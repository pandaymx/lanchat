package ipc

import (
	"encoding/json"

	"github.com/pandaymx/lanchat/internal/appapi"
)

// rpcErr 是 handler 返回的 JSON-RPC 业务错误。
type rpcErr struct {
	code    int
	message string
}

// handler 处理一个 JSON-RPC 方法：解码 params，调用 appapi.API，返回 result。
type handler func(api appapi.API, params json.RawMessage) (interface{}, *rpcErr)

// handlers 方法名 → handler，方法集合与 api/ipc.schema.json requests 完全一致。
var handlers = map[string]handler{
	"GetState": func(api appapi.API, _ json.RawMessage) (interface{}, *rpcErr) {
		return api.GetState(), nil
	},
	"Connect": hConnect,
	"BrowseServers": func(api appapi.API, _ json.RawMessage) (interface{}, *rpcErr) {
		return api.BrowseServers(), nil
	},
	"SendText":         hSendText,
	"SendSticker":      hSendSticker,
	"OfferFile":        hOfferFile,
	"OfferFileToGroup": hOfferFileToGroup,
	"RespondFile":      hRespondFile,
	"PauseFile":        hPauseFile,
	"ResumeFile":       hResumeFile,
	"CancelFile":       hCancelFile,
	"SetNickname":      hSetNickname,
	"PickDownloadDir":  hPickDownloadDir,
	"ChannelCreate":    hChannelCreate,
	"ChannelJoin":      hChannelJoin,
	"ChannelInvite":    hChannelInvite,
	"ChannelLeave":     hChannelLeave,
	"ChannelList": func(api appapi.API, _ json.RawMessage) (interface{}, *rpcErr) {
		return api.ChannelList(), nil
	},
}

// decodeParams 解码 params 并在失败时返回 invalid params 错误。
func decodeParams[T any](raw json.RawMessage) (T, *rpcErr) {
	var p T
	if len(raw) == 0 {
		return p, &rpcErr{code: codeInvalidParam, message: "缺少 params"}
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, &rpcErr{code: codeInvalidParam, message: "params 非法: " + err.Error()}
	}
	return p, nil
}

// apiErr 把 core 返回的普通错误包装为 JSON-RPC internal 错误。
func apiErr(err error) *rpcErr {
	return &rpcErr{code: codeInternal, message: err.Error()}
}

type connectParams struct {
	Addr string `json:"addr"`
	PSK  string `json:"psk"`
}

func hConnect(api appapi.API, raw json.RawMessage) (interface{}, *rpcErr) {
	p, rerr := decodeParams[connectParams](raw)
	if rerr != nil {
		return nil, rerr
	}
	if err := api.Connect(p.Addr, p.PSK); err != nil {
		return nil, apiErr(err)
	}
	return struct{}{}, nil
}

type sendTextParams struct {
	To    string `json:"to"`
	Text  string `json:"text"`
	Group string `json:"group"`
}

func hSendText(api appapi.API, raw json.RawMessage) (interface{}, *rpcErr) {
	p, rerr := decodeParams[sendTextParams](raw)
	if rerr != nil {
		return nil, rerr
	}
	id, err := api.SendText(p.To, p.Text, p.Group)
	if err != nil {
		return nil, apiErr(err)
	}
	return struct {
		MsgID string `json:"msgID"`
	}{MsgID: id}, nil
}

type sendStickerParams struct {
	To   string `json:"to"`
	Path string `json:"path"`
}

func hSendSticker(api appapi.API, raw json.RawMessage) (interface{}, *rpcErr) {
	p, rerr := decodeParams[sendStickerParams](raw)
	if rerr != nil {
		return nil, rerr
	}
	id, err := api.SendSticker(p.To, p.Path)
	if err != nil {
		return nil, apiErr(err)
	}
	return struct {
		MsgID string `json:"msgID"`
	}{MsgID: id}, nil
}

type offerFileParams struct {
	To   string `json:"to"`
	Path string `json:"path"`
}

func hOfferFile(api appapi.API, raw json.RawMessage) (interface{}, *rpcErr) {
	p, rerr := decodeParams[offerFileParams](raw)
	if rerr != nil {
		return nil, rerr
	}
	id, err := api.OfferFile(p.To, p.Path)
	if err != nil {
		return nil, apiErr(err)
	}
	return struct {
		TransferID string `json:"transferID"`
	}{TransferID: id}, nil
}

type offerFileToGroupParams struct {
	Group string `json:"group"`
	Path  string `json:"path"`
}

func hOfferFileToGroup(api appapi.API, raw json.RawMessage) (interface{}, *rpcErr) {
	p, rerr := decodeParams[offerFileToGroupParams](raw)
	if rerr != nil {
		return nil, rerr
	}
	id, err := api.OfferFileToGroup(p.Group, p.Path)
	if err != nil {
		return nil, apiErr(err)
	}
	return struct {
		TransferID string `json:"transferID"`
	}{TransferID: id}, nil
}

type respondFileParams struct {
	TransferID string `json:"transferID"`
	Accept     bool   `json:"accept"`
	Dest       string `json:"dest"`
}

func hRespondFile(api appapi.API, raw json.RawMessage) (interface{}, *rpcErr) {
	p, rerr := decodeParams[respondFileParams](raw)
	if rerr != nil {
		return nil, rerr
	}
	if err := api.RespondFile(p.TransferID, p.Accept, p.Dest); err != nil {
		return nil, apiErr(err)
	}
	return struct{}{}, nil
}

type transferIDParams struct {
	TransferID string `json:"transferID"`
}

func hPauseFile(api appapi.API, raw json.RawMessage) (interface{}, *rpcErr) {
	p, rerr := decodeParams[transferIDParams](raw)
	if rerr != nil {
		return nil, rerr
	}
	if err := api.PauseFile(p.TransferID); err != nil {
		return nil, apiErr(err)
	}
	return struct{}{}, nil
}

func hResumeFile(api appapi.API, raw json.RawMessage) (interface{}, *rpcErr) {
	p, rerr := decodeParams[transferIDParams](raw)
	if rerr != nil {
		return nil, rerr
	}
	if err := api.ResumeFile(p.TransferID); err != nil {
		return nil, apiErr(err)
	}
	return struct{}{}, nil
}

func hCancelFile(api appapi.API, raw json.RawMessage) (interface{}, *rpcErr) {
	p, rerr := decodeParams[transferIDParams](raw)
	if rerr != nil {
		return nil, rerr
	}
	if err := api.CancelFile(p.TransferID); err != nil {
		return nil, apiErr(err)
	}
	return struct{}{}, nil
}

type setNicknameParams struct {
	Name string `json:"name"`
}

func hSetNickname(api appapi.API, raw json.RawMessage) (interface{}, *rpcErr) {
	p, rerr := decodeParams[setNicknameParams](raw)
	if rerr != nil {
		return nil, rerr
	}
	if err := api.SetNickname(p.Name); err != nil {
		return nil, apiErr(err)
	}
	return struct{}{}, nil
}

type pickDownloadDirParams struct {
	Path string `json:"path"`
}

func hPickDownloadDir(api appapi.API, raw json.RawMessage) (interface{}, *rpcErr) {
	p, rerr := decodeParams[pickDownloadDirParams](raw)
	if rerr != nil {
		return nil, rerr
	}
	if err := api.PickDownloadDir(p.Path); err != nil {
		return nil, apiErr(err)
	}
	return struct{}{}, nil
}

type channelCreateParams struct {
	Name    string `json:"name"`
	Topic   string `json:"topic"`
	Private bool   `json:"private"`
}

func hChannelCreate(api appapi.API, raw json.RawMessage) (interface{}, *rpcErr) {
	p, rerr := decodeParams[channelCreateParams](raw)
	if rerr != nil {
		return nil, rerr
	}
	id, err := api.ChannelCreate(p.Name, p.Topic, p.Private)
	if err != nil {
		return nil, apiErr(err)
	}
	return struct {
		ChannelID string `json:"channelID"`
	}{ChannelID: id}, nil
}

type channelJoinParams struct {
	ChannelID string `json:"channelID"`
}

func hChannelJoin(api appapi.API, raw json.RawMessage) (interface{}, *rpcErr) {
	p, rerr := decodeParams[channelJoinParams](raw)
	if rerr != nil {
		return nil, rerr
	}
	if err := api.ChannelJoin(p.ChannelID); err != nil {
		return nil, apiErr(err)
	}
	return struct{}{}, nil
}

type channelInviteParams struct {
	ChannelID string `json:"channelID"`
	MemberID  string `json:"memberID"`
}

func hChannelInvite(api appapi.API, raw json.RawMessage) (interface{}, *rpcErr) {
	p, rerr := decodeParams[channelInviteParams](raw)
	if rerr != nil {
		return nil, rerr
	}
	if err := api.ChannelInvite(p.ChannelID, p.MemberID); err != nil {
		return nil, apiErr(err)
	}
	return struct{}{}, nil
}

func hChannelLeave(api appapi.API, raw json.RawMessage) (interface{}, *rpcErr) {
	p, rerr := decodeParams[channelJoinParams](raw)
	if rerr != nil {
		return nil, rerr
	}
	if err := api.ChannelLeave(p.ChannelID); err != nil {
		return nil, apiErr(err)
	}
	return struct{}{}, nil
}
