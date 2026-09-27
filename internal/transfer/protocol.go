// Package transfer 实现文件 P2P TCP 直连的主干传输。
//
// 它复用 M0 冻结的 LCTP 二进制帧（internal/protocol），在本包内约定各帧
// payload 的编码：
//
//   - 握手/控制帧（HELLO/ACCEPT/FIN/ERROR/CANCEL）payload 用 JSON；
//   - DATA payload 为「offset int64（8B 大端）+ data」；
//   - ACK payload 为「cumOffset int64（8B 大端）+ credit uint32（4B 大端）」。
//
// M2 只做单发送方、顺序字节流与全文件 SHA-256 校验；续传与块对齐留待 M3。
package transfer

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/pandaymx/lanchat/internal/protocol"
)

// 帧负载过小/格式错误。
var (
	errShortPayload = errors.New("transfer: short payload")
	errBadPayload   = errors.New("transfer: malformed payload")
)

// helloBody 是 HELLO 帧的 JSON 负载。ResumeOffset 在 M2 恒为 0，M3 续传启用。
type helloBody struct {
	Token        string `json:"token"`
	ResumeOffset int64  `json:"resumeOffset"`
}

// acceptBody 是 ACCEPT 帧的 JSON 负载。
type acceptBody struct {
	Size      int64 `json:"size"`
	ChunkSize int   `json:"chunkSize"`
}

// finBody 是 FIN 帧的 JSON 负载。
type finBody struct {
	SHA256     string `json:"sha256"`
	TotalBytes int64  `json:"totalBytes"`
}

// errBody 是 ERROR 帧的 JSON 负载。
type errBody struct {
	Reason string `json:"reason"`
}

// writeJSON 把 v 以 JSON 编码后作为指定类型帧写出。
func writeJSON(fr *protocol.Framer, frameType byte, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return fr.WriteFrame(&protocol.Frame{Type: frameType, Payload: payload})
}

// readJSON 读一帧并把其 JSON 负载解码到 v，同时校验帧类型。
func readJSON(fr *protocol.Framer, frameType byte, v any) error {
	f, err := fr.ReadFrame()
	if err != nil {
		return err
	}
	if f.Type != frameType {
		if f.Type == protocol.FrameError {
			var eb errBody
			_ = json.Unmarshal(f.Payload, &eb)
			return fmt.Errorf("%w: %s", ErrPeerError, eb.Reason)
		}
		return fmt.Errorf("%w: got type %#x want %#x", errBadPayload, f.Type, frameType)
	}
	if len(f.Payload) == 0 {
		return errShortPayload
	}
	if err := json.Unmarshal(f.Payload, v); err != nil {
		return fmt.Errorf("%w: %v", errBadPayload, err)
	}
	return nil
}

// writeHello 发送 HELLO 帧。
func writeHello(fr *protocol.Framer, token string, resumeOffset int64) error {
	return writeJSON(fr, protocol.FrameHello, helloBody{Token: token, ResumeOffset: resumeOffset})
}

// readHello 读取 HELLO 帧。
func readHello(fr *protocol.Framer) (helloBody, error) {
	var b helloBody
	err := readJSON(fr, protocol.FrameHello, &b)
	return b, err
}

// writeAccept 发送 ACCEPT 帧。
func writeAccept(fr *protocol.Framer, size int64, chunkSize int) error {
	return writeJSON(fr, protocol.FrameAccept, acceptBody{Size: size, ChunkSize: chunkSize})
}

// readAccept 读取 ACCEPT 帧。
func readAccept(fr *protocol.Framer) (acceptBody, error) {
	var b acceptBody
	err := readJSON(fr, protocol.FrameAccept, &b)
	return b, err
}

// writeFin 发送 FIN 帧。
func writeFin(fr *protocol.Framer, sha256 string, totalBytes int64) error {
	return writeJSON(fr, protocol.FrameFin, finBody{SHA256: sha256, TotalBytes: totalBytes})
}

// readFin 读取 FIN 帧。
func readFin(fr *protocol.Framer) (finBody, error) {
	var b finBody
	err := readJSON(fr, protocol.FrameFin, &b)
	return b, err
}

// writePeerError 发送 ERROR 帧。
func writePeerError(fr *protocol.Framer, reason string) error {
	return writeJSON(fr, protocol.FrameError, errBody{Reason: reason})
}

// writeCancel 发送 CANCEL 帧。
func writeCancel(fr *protocol.Framer) error {
	return writeJSON(fr, protocol.FrameCancel, errBody{Reason: "canceled"})
}

// dataHeaderSize DATA 帧内 offset 头部长度。
const dataHeaderSize = 8

// writeData 发送 DATA 帧：offset（8B 大端）+ data。
func writeData(fr *protocol.Framer, offset int64, data []byte) error {
	payload := make([]byte, dataHeaderSize+len(data))
	binary.BigEndian.PutUint64(payload[:dataHeaderSize], uint64(offset))
	copy(payload[dataHeaderSize:], data)
	return fr.WriteFrame(&protocol.Frame{Type: protocol.FrameData, Payload: payload})
}

// readData 读取 DATA 帧，返回 offset 与数据副本。
func readData(fr *protocol.Framer) (offset int64, data []byte, err error) {
	f, err := fr.ReadFrame()
	if err != nil {
		return 0, nil, err
	}
	if f.Type == protocol.FrameCancel {
		return 0, nil, ErrCanceled
	}
	if f.Type == protocol.FrameError {
		var eb errBody
		_ = json.Unmarshal(f.Payload, &eb)
		return 0, nil, fmt.Errorf("%w: %s", ErrPeerError, eb.Reason)
	}
	if f.Type != protocol.FrameData {
		return 0, nil, fmt.Errorf("%w: got type %#x want data", errBadPayload, f.Type)
	}
	if len(f.Payload) < dataHeaderSize {
		return 0, nil, errShortPayload
	}
	off := int64(binary.BigEndian.Uint64(f.Payload[:dataHeaderSize]))
	body := make([]byte, len(f.Payload)-dataHeaderSize)
	copy(body, f.Payload[dataHeaderSize:])
	return off, body, nil
}

// ackHeaderSize ACK 帧头部长度：cumOffset 8B + credit 4B。
const ackHeaderSize = 12

// writeAck 发送 ACK 帧：cumOffset（8B 大端）+ credit（4B 大端）。
func writeAck(fr *protocol.Framer, cumOffset int64, credit uint32) error {
	payload := make([]byte, ackHeaderSize)
	binary.BigEndian.PutUint64(payload[:8], uint64(cumOffset))
	binary.BigEndian.PutUint32(payload[8:12], credit)
	return fr.WriteFrame(&protocol.Frame{Type: protocol.FrameAck, Payload: payload})
}

// readAck 读取 ACK 帧。
func readAck(fr *protocol.Framer) (cumOffset int64, credit uint32, err error) {
	f, err := fr.ReadFrame()
	if err != nil {
		return 0, 0, err
	}
	if f.Type == protocol.FrameCancel {
		return 0, 0, ErrCanceled
	}
	if f.Type == protocol.FrameError {
		var eb errBody
		_ = json.Unmarshal(f.Payload, &eb)
		return 0, 0, fmt.Errorf("%w: %s", ErrPeerError, eb.Reason)
	}
	if f.Type != protocol.FrameAck {
		return 0, 0, fmt.Errorf("%w: got type %#x want ack", errBadPayload, f.Type)
	}
	if len(f.Payload) < ackHeaderSize {
		return 0, 0, errShortPayload
	}
	cum := int64(binary.BigEndian.Uint64(f.Payload[:8]))
	cred := binary.BigEndian.Uint32(f.Payload[8:12])
	return cum, cred, nil
}
