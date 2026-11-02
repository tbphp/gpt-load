package embedded

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
)

// 数据帧直接分段透传；只有结束帧需要在 CPA 解析、记录和转换之前完整校验。
type devinResponseBody struct {
	io.ReadCloser
	secrets   []string
	pending   []byte
	remaining int64
	ended     bool
}

func (body *devinResponseBody) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	for {
		if len(body.pending) > 0 {
			n := copy(buffer, body.pending)
			body.pending = body.pending[n:]
			return n, nil
		}
		if body.remaining > 0 {
			limit := min(int64(len(buffer)), body.remaining)
			n, err := body.ReadCloser.Read(buffer[:limit])
			body.remaining -= int64(n)
			if err == io.EOF {
				if body.remaining > 0 {
					err = io.ErrUnexpectedEOF
				} else {
					err = nil
				}
			}
			return n, err
		}
		if body.ended {
			return 0, io.EOF
		}
		var header [5]byte
		if _, err := io.ReadFull(body.ReadCloser, header[:]); err != nil {
			return 0, err
		}
		length := binary.BigEndian.Uint32(header[1:])
		if length > devinMaxConnectFrameBytes {
			return 0, invalidDevinTrailer()
		}
		if header[0]&helps.ConnectFlagEndStream == 0 {
			body.pending = header[:]
			body.remaining = int64(length)
			continue
		}
		body.ended = true
		frame := make([]byte, 5+int(length))
		copy(frame, header[:])
		if _, err := io.ReadFull(body.ReadCloser, frame[5:]); err != nil {
			return 0, err
		}
		flag, trailer, err := helps.ReadConnectFrame(bytes.NewReader(frame))
		if err != nil {
			return 0, invalidDevinTrailer()
		}
		trailer, err = normalizeDevinTrailer(trailer, body.secrets)
		if err != nil {
			return 0, err
		}
		// ReadConnectFrame 已解压结束帧，重新封装时不能保留压缩位。
		body.pending = helps.WrapConnectEnvelopeWithFlag(flag & ^helps.ConnectFlagCompressed, trailer)
	}
}

func normalizeDevinTrailer(payload []byte, secrets []string) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil || fields == nil {
		return nil, invalidDevinTrailer()
	}
	for name := range fields {
		// CPA 的 JSON 结构体会接受大小写别名；拒绝会与标准 error 字段产生歧义的输入。
		if name != "error" && strings.EqualFold(name, "error") {
			return nil, invalidDevinTrailer()
		}
	}
	raw, present := fields["error"]
	if !present || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return payload, nil
	}
	var upstream struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &upstream); err != nil {
		return nil, invalidDevinTrailer()
	}
	for _, secret := range secrets {
		if secret != "" {
			upstream.Code = strings.ReplaceAll(upstream.Code, secret, "[REDACTED]")
			upstream.Message = strings.ReplaceAll(upstream.Message, secret, "[REDACTED]")
		}
	}
	// 先解码字符串再脱敏，覆盖 JSON 转义；保留配额等错误文本供 CPA 分类。
	safeError, err := json.Marshal(upstream)
	if err != nil {
		return nil, invalidDevinTrailer()
	}
	fields["error"] = safeError
	return json.Marshal(fields)
}

func invalidDevinTrailer() error {
	return &DevinExecutionError{status: 502, code: "upstream_stream_incomplete", summary: "Devin returned an invalid Connect terminal frame"}
}
