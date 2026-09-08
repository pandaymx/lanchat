// Package libsql 提供 core.Store 的 libSQL 持久化实现（ADR-013）。
//
// 驱动只用纯 Go 的 github.com/tursodatabase/libsql-client-go（本地文件
// 模式底层即 modernc sqlite，零 CGO，可交叉编译 / gomobile bind）；
// 禁止 CGO 绑定 go-libsql。访问走标准库 database/sql，手写 SQL，不引 ORM。
//
// 建表用 CREATE TABLE IF NOT EXISTS 幂等迁移：M5 只有一个 schema 版本，
// 不引入 migration 框架；将来改表结构时按 AGENTS.md §5.2「迁移与逻辑分离」
// 单独出 commit。
//
// 与 memory 实现的语义对齐：
//   - AppendMessage 按 (conv_id, id) upsert（同 ID 覆盖，ServerSeq 由 Hub
//     补齐后不会变两条）；
//   - History 按 server_seq 升序、严格大于 after、limit 截断；
//   - SetCursor 单调不回退；GetCursor 未设置返回 0；
//   - Get* 未命中返回 core.ErrNotFound。
package libsql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	// 纯 Go libSQL 驱动（本地模式 = modernc sqlite），匿名注册到 database/sql。
	_ "github.com/tursodatabase/libsql-client-go/libsql"
	// libsql 本地文件模式（file: DSN）自身不带引擎，要求 sql.Drivers() 里
	// 存在 "sqlite"（modernc，纯 Go）或 "sqlite3"（mattn，CGO，禁用）。
	// 显式 blank import modernc 完成注册——这也是 ADR-013「禁 CGO」的落点。
	_ "modernc.org/sqlite"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// errMissingConv / errInvalidCursor 与 memory 实现的输入校验同义。
var (
	errInvalidCursor = errors.New("libsql: device id and conv id required")
	errMissingFileID = errors.New("libsql: file id required")
)

// nowMillis 抽成函数是为了和 memory 实现保持同一时间口径（Unix 毫秒）。
func nowMillis() int64 { return time.Now().UnixMilli() }

// driverName 是 database/sql 注册名（libsql-client-go 固定为 "libsql"）。
const driverName = "libsql"

// maxHistoryLimit 与 memory 实现一致：单次 History 的硬上限，防 OOM。
const maxHistoryLimit = 5000

// Store 是 core.Store 的 libSQL 实现。零值不可用，请用 Open。
type Store struct {
	db *sql.DB
}

// 编译期断言：libSQL Store 满足 core.Store（ADR-002 开关点契约）。
var _ core.Store = (*Store)(nil)

// Open 打开（必要时创建）DSN 指定的库并完成建表迁移。
//
// DSN 形如 "file:/var/lib/lanchat/hub.db"；":memory:" 给单测用。
// 打开后立即 Ping 一次，把「文件不可写 / 驱动缺失」这类问题暴露在启动期。
func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("libsql: open %q: %w", dsn, err)
	}
	// SQLite 单文件写串行；限制连接数避免 "database is locked"。
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("libsql: ping %q: %w", dsn, err)
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// migrate 执行幂等建表。顺序无依赖，IF NOT EXISTS 保证重复执行安全。
func (s *Store) migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL,
			avatar_seed TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS devices (
			id      TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			name    TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS conversations (
			id    TEXT PRIMARY KEY,
			kind  TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			conv_id       TEXT NOT NULL,
			id            TEXT NOT NULL,
			server_seq    INTEGER NOT NULL,
			client_nonce  TEXT NOT NULL DEFAULT '',
			sender_user   TEXT NOT NULL DEFAULT '',
			sender_device TEXT NOT NULL DEFAULT '',
			body          TEXT NOT NULL DEFAULT '',
			created_at    INTEGER NOT NULL,
			PRIMARY KEY (conv_id, id)
		)`,
		// 补发查询的固定模式：WHERE conv_id=? AND server_seq>? ORDER BY server_seq
		`CREATE INDEX IF NOT EXISTS idx_messages_conv_seq ON messages (conv_id, server_seq)`,
		`CREATE TABLE IF NOT EXISTS read_cursors (
			device_id TEXT NOT NULL,
			conv_id   TEXT NOT NULL,
			last_seq  INTEGER NOT NULL,
			PRIMARY KEY (device_id, conv_id)
		)`,
		// M12-A 群成员：会话成员表（大厅是隐式会话不落库）。
		`CREATE TABLE IF NOT EXISTS conversation_members (
			conv_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			PRIMARY KEY (conv_id, user_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_conv_members_user ON conversation_members (user_id)`,
		// M9 文件元信息：blob 本体在 hub 文件目录（files/<FileID>），
		// 这里只保证「重启后按 ID 能查到」；CreatedAt 供未来清理/审计。
		`CREATE TABLE IF NOT EXISTS file_meta (
			file_id    TEXT PRIMARY KEY,
			name       TEXT NOT NULL,
			size       INTEGER NOT NULL,
			mime       TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL
		)`,
	}
	for i, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("libsql: migrate stmt %d: %w", i, err)
		}
	}
	// messages 附件列（M9）：老库（M8 及以前）的 messages 表没有这些列，
	// CREATE TABLE IF NOT EXISTS 不会补列；PRAGMA table_info 幂等补加。
	// 列名来自内部常量表，不拼接任何外部输入，无注入面。
	for _, col := range []string{"file_id", "file_name", "file_size", "file_mime"} {
		has, err := s.hasColumn(ctx, "messages", col)
		if err != nil {
			return err
		}
		if !has {
			if _, err := s.db.ExecContext(ctx,
				`ALTER TABLE messages ADD COLUMN `+col+` TEXT NOT NULL DEFAULT ''`); err != nil {
				return fmt.Errorf("libsql: migrate add column %s: %w", col, err)
			}
		}
	}
	return nil
}

// hasColumn 用 PRAGMA table_info 判断列是否存在（幂等迁移用）。
func (s *Store) hasColumn(ctx context.Context, table, column string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, fmt.Errorf("libsql: table_info %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, fmt.Errorf("libsql: scan table_info: %w", err)
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("libsql: iterate table_info: %w", err)
	}
	return false, nil
}

// Close 关闭连接池。重复调用由 database/sql 兜底（返回 ErrConnDone 类错误，忽略）。
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
		return err
	}
	return nil
}

// ---- users / devices / conversations ---------------------------------------

// SaveUser 保存或覆盖一个用户。
func (s *Store) SaveUser(ctx context.Context, u protocol.User) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, name, avatar_seed) VALUES (?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET name = excluded.name, avatar_seed = excluded.avatar_seed`,
		u.ID, u.Name, u.AvatarSeed)
	if err != nil {
		return fmt.Errorf("libsql: save user %q: %w", u.ID, err)
	}
	return nil
}

// GetUser 读取用户；未命中返回 core.ErrNotFound。
func (s *Store) GetUser(ctx context.Context, id string) (protocol.User, error) {
	var u protocol.User
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, avatar_seed FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Name, &u.AvatarSeed)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.User{}, core.ErrNotFound
	}
	if err != nil {
		return protocol.User{}, fmt.Errorf("libsql: get user %q: %w", id, err)
	}
	return u, nil
}

// SaveDevice 保存或覆盖一个设备。
func (s *Store) SaveDevice(ctx context.Context, d protocol.Device) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO devices (id, user_id, name) VALUES (?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET user_id = excluded.user_id, name = excluded.name`,
		d.ID, d.UserID, d.Name)
	if err != nil {
		return fmt.Errorf("libsql: save device %q: %w", d.ID, err)
	}
	return nil
}

// GetDevice 读取设备；未命中返回 core.ErrNotFound。
func (s *Store) GetDevice(ctx context.Context, id string) (protocol.Device, error) {
	var d protocol.Device
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, name FROM devices WHERE id = ?`, id).
		Scan(&d.ID, &d.UserID, &d.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.Device{}, core.ErrNotFound
	}
	if err != nil {
		return protocol.Device{}, fmt.Errorf("libsql: get device %q: %w", id, err)
	}
	return d, nil
}

// SaveConversation 保存或覆盖一个会话。
func (s *Store) SaveConversation(ctx context.Context, c protocol.Conversation) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO conversations (id, kind, title) VALUES (?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET kind = excluded.kind, title = excluded.title`,
		c.ID, c.Kind, c.Title)
	if err != nil {
		return fmt.Errorf("libsql: save conversation %q: %w", c.ID, err)
	}
	return nil
}

// GetConversation 读取会话；未命中返回 core.ErrNotFound。
func (s *Store) GetConversation(ctx context.Context, id string) (protocol.Conversation, error) {
	var c protocol.Conversation
	err := s.db.QueryRowContext(ctx,
		`SELECT id, kind, title FROM conversations WHERE id = ?`, id).
		Scan(&c.ID, &c.Kind, &c.Title)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.Conversation{}, core.ErrNotFound
	}
	if err != nil {
		return protocol.Conversation{}, fmt.Errorf("libsql: get conversation %q: %w", id, err)
	}
	return c, nil
}

// ListConversations 返回全部已持久化的会话（M12-A）。
func (s *Store) ListConversations(ctx context.Context) ([]protocol.Conversation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, title FROM conversations ORDER BY rowid`)
	if err != nil {
		return nil, fmt.Errorf("libsql: list conversations: %w", err)
	}
	defer rows.Close()
	var out []protocol.Conversation
	for rows.Next() {
		var c protocol.Conversation
		if err := rows.Scan(&c.ID, &c.Kind, &c.Title); err != nil {
			return nil, fmt.Errorf("libsql: scan conversation: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveConversationMember 记录某用户加入某会话（M12-A，幂等）。
func (s *Store) SaveConversationMember(ctx context.Context, convID, userID string) error {
	if convID == "" || userID == "" {
		return nil // 大厅/空身份不落库
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO conversation_members (conv_id, user_id) VALUES (?, ?)
		 ON CONFLICT(conv_id, user_id) DO NOTHING`,
		convID, userID)
	if err != nil {
		return fmt.Errorf("libsql: save member %q/%q: %w", convID, userID, err)
	}
	return nil
}

// ListConversationMembers 返回某会话的全部成员 UserID（M12-A）。
func (s *Store) ListConversationMembers(ctx context.Context, convID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_id FROM conversation_members WHERE conv_id = ? ORDER BY user_id`, convID)
	if err != nil {
		return nil, fmt.Errorf("libsql: list members %q: %w", convID, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, fmt.Errorf("libsql: scan member: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// DeleteConversationMember 移除某用户出会话（M12-A 退群）。
func (s *Store) DeleteConversationMember(ctx context.Context, convID, userID string) error {
	if convID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM conversation_members WHERE conv_id = ? AND user_id = ?`, convID, userID)
	if err != nil {
		return fmt.Errorf("libsql: delete member %q/%q: %w", convID, userID, err)
	}
	return nil
}

// ---- messages ---------------------------------------------------------------

// AppendMessage 追加或按 (conv_id, id) 覆盖一条消息。
//
// 与 memory 实现同为 upsert 语义：客户端乐观写入 (ID=local-x, seq=0) 后
// Hub 回 FKDeliver (同 ID, seq=N)，覆盖更新而不是插成两条。
func (s *Store) AppendMessage(ctx context.Context, m protocol.StoredMessage) error {
	if m.CreatedAt == 0 {
		m.CreatedAt = nowMillis()
	}
	var fileID, fileName, fileMime string
	var fileSize int64
	if m.File != nil {
		fileID, fileName, fileSize, fileMime = m.File.FileID, m.File.Name, m.File.Size, m.File.Mime
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO messages
		   (conv_id, id, server_seq, client_nonce, sender_user, sender_device, body, created_at,
		    file_id, file_name, file_size, file_mime)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(conv_id, id) DO UPDATE SET
		   server_seq    = excluded.server_seq,
		   client_nonce  = excluded.client_nonce,
		   sender_user   = excluded.sender_user,
		   sender_device = excluded.sender_device,
		   body          = excluded.body,
		   created_at    = excluded.created_at,
		   file_id       = excluded.file_id,
		   file_name     = excluded.file_name,
		   file_size     = excluded.file_size,
		   file_mime     = excluded.file_mime`,
		m.ConversationID, m.ID, int64(m.ServerSeq), m.ClientNonce,
		m.SenderUserID, m.SenderDeviceID, m.Body, m.CreatedAt,
		fileID, fileName, fileSize, fileMime)
	if err != nil {
		return fmt.Errorf("libsql: append message %q/%q: %w", m.ConversationID, m.ID, err)
	}
	return nil
}

// History 返回 (convID, after) 之后按 ServerSeq 升序的消息，最多 limit 条。
// limit<=0 时取 maxHistoryLimit 硬上限（与 memory 实现一致防 OOM）。
func (s *Store) History(ctx context.Context, convID string, after uint64, limit int) ([]protocol.StoredMessage, error) {
	if limit <= 0 || limit > maxHistoryLimit {
		limit = maxHistoryLimit
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, client_nonce, conv_id, sender_user, sender_device, body, server_seq, created_at,
		        file_id, file_name, file_size, file_mime
		 FROM messages
		 WHERE conv_id = ? AND server_seq > ?
		 ORDER BY server_seq ASC
		 LIMIT ?`,
		convID, int64(after), limit)
	if err != nil {
		return nil, fmt.Errorf("libsql: history conv %q after %d: %w", convID, after, err)
	}
	defer func() { _ = rows.Close() }()
	return scanMessages(rows)
}

// MaxSeq 返回全库已落库消息的最大 ServerSeq；无消息返回 0。
//
// Hub 重启恢复时把它作为 RouterConfig.StartSeq，避免新序号与历史撞车。
func (s *Store) MaxSeq(ctx context.Context) (uint64, error) {
	var maxSeq sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(server_seq) FROM messages`).Scan(&maxSeq); err != nil {
		return 0, fmt.Errorf("libsql: max seq: %w", err)
	}
	if !maxSeq.Valid || maxSeq.Int64 < 0 {
		return 0, nil
	}
	return uint64(maxSeq.Int64), nil
}

// RecentMessages 返回全库最近 limit 条消息（跨会话），按 ServerSeq 升序。
//
// Hub 重启后把它灌回内存补发缓冲（hubstate.History）：缓冲有界（每会话
// 5000 条），limit 由调用方按缓冲容量给。
func (s *Store) RecentMessages(ctx context.Context, limit int) ([]protocol.StoredMessage, error) {
	if limit <= 0 || limit > maxHistoryLimit {
		limit = maxHistoryLimit
	}
	// 先 DESC 取最近 N 条，再在 Go 侧反转成升序——补发缓冲要求升序追加。
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, client_nonce, conv_id, sender_user, sender_device, body, server_seq, created_at,
		        file_id, file_name, file_size, file_mime
		 FROM messages
		 ORDER BY server_seq DESC
		 LIMIT ?`,
		limit)
	if err != nil {
		return nil, fmt.Errorf("libsql: recent messages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// scanMessages 消费 rows 到 StoredMessage 切片（升序由 SQL 保证）。
// M9：读取附件四列，file_id 非空时组装 FileRef（老库该列默认空串）。
func scanMessages(rows *sql.Rows) ([]protocol.StoredMessage, error) {
	var out []protocol.StoredMessage
	for rows.Next() {
		var m protocol.StoredMessage
		var seq int64
		var fileID, fileName, fileMime string
		var fileSize int64
		if err := rows.Scan(
			&m.ID, &m.ClientNonce, &m.ConversationID,
			&m.SenderUserID, &m.SenderDeviceID, &m.Body,
			&seq, &m.CreatedAt,
			&fileID, &fileName, &fileSize, &fileMime,
		); err != nil {
			return nil, fmt.Errorf("libsql: scan message: %w", err)
		}
		m.ServerSeq = uint64(seq)
		if fileID != "" {
			m.File = &protocol.FileRef{FileID: fileID, Name: fileName, Size: fileSize, Mime: fileMime}
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("libsql: iterate messages: %w", err)
	}
	return out, nil
}

// ---- file meta (M9) ----------------------------------------------------------

// SaveFileMeta 记录文件元信息，幂等覆盖（同 FileID 重复上传会换 ID，正常路径不会撞）。
func (s *Store) SaveFileMeta(ctx context.Context, m protocol.FileMeta) error {
	if m.FileID == "" {
		return errMissingFileID
	}
	if m.CreatedAt == 0 {
		m.CreatedAt = nowMillis()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO file_meta (file_id, name, size, mime, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(file_id) DO UPDATE SET
		   name = excluded.name, size = excluded.size,
		   mime = excluded.mime, created_at = excluded.created_at`,
		m.FileID, m.Name, m.Size, m.Mime, m.CreatedAt)
	if err != nil {
		return fmt.Errorf("libsql: save file meta %q: %w", m.FileID, err)
	}
	return nil
}

// GetFileMeta 按 FileID 读取文件元信息；未命中返回 core.ErrNotFound。
func (s *Store) GetFileMeta(ctx context.Context, fileID string) (protocol.FileMeta, error) {
	var m protocol.FileMeta
	err := s.db.QueryRowContext(ctx,
		`SELECT file_id, name, size, mime, created_at FROM file_meta WHERE file_id = ?`, fileID).
		Scan(&m.FileID, &m.Name, &m.Size, &m.Mime, &m.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.FileMeta{}, core.ErrNotFound
	}
	if err != nil {
		return protocol.FileMeta{}, fmt.Errorf("libsql: get file meta %q: %w", fileID, err)
	}
	return m, nil
}

// ---- read cursors -----------------------------------------------------------

// SetCursor 设置某设备在某会话的已读游标，单调不回退（UPSERT + MAX）。
func (s *Store) SetCursor(ctx context.Context, deviceID, convID string, seq uint64) error {
	if deviceID == "" {
		return errInvalidCursor
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO read_cursors (device_id, conv_id, last_seq) VALUES (?, ?, ?)
		 ON CONFLICT(device_id, conv_id) DO UPDATE SET
		   last_seq = MAX(read_cursors.last_seq, excluded.last_seq)`,
		deviceID, convID, int64(seq))
	if err != nil {
		return fmt.Errorf("libsql: set cursor %s/%s: %w", deviceID, convID, err)
	}
	return nil
}

// GetCursor 返回某设备在某会话的已读游标；未设置返回 0（语义：从未读过）。
func (s *Store) GetCursor(ctx context.Context, deviceID, convID string) (uint64, error) {
	if deviceID == "" {
		return 0, errInvalidCursor
	}
	var seq sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT last_seq FROM read_cursors WHERE device_id = ? AND conv_id = ?`,
		deviceID, convID).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("libsql: get cursor %s/%s: %w", deviceID, convID, err)
	}
	if !seq.Valid || seq.Int64 < 0 {
		return 0, nil
	}
	return uint64(seq.Int64), nil
}

// ListCursors 枚举已记录的已读游标（M8.1）。convID 非空时只返回该会话。
func (s *Store) ListCursors(ctx context.Context, convID string) ([]protocol.ReadCursor, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if convID != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT device_id, conv_id, last_seq FROM read_cursors
			 WHERE conv_id = ? ORDER BY device_id`, convID)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT device_id, conv_id, last_seq FROM read_cursors
			 ORDER BY device_id, conv_id`)
	}
	if err != nil {
		return nil, fmt.Errorf("libsql: list cursors: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []protocol.ReadCursor
	for rows.Next() {
		var device, conv string
		var seq int64
		if err := rows.Scan(&device, &conv, &seq); err != nil {
			return nil, fmt.Errorf("libsql: scan cursor row: %w", err)
		}
		if seq < 0 {
			seq = 0
		}
		out = append(out, protocol.ReadCursor{
			DeviceID:       device,
			ConversationID: conv,
			ServerSeq:      uint64(seq),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("libsql: iterate cursors: %w", err)
	}
	return out, nil
}
