package mirasim

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const maxCodexEventBytes = 52_428_800

// codexNonStreamPayload extracts the terminal response event from the Codex
// SSE wire protocol. Mirasim accepts the real Codex request shape, which uses
// stream=true even when CLIProxyAPI needs to return one downstream JSON body.
func codexNonStreamPayload(raw []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("Mirasim Codex response is empty")
	}
	if json.Valid(trimmed) {
		var probe struct {
			Type   string          `json:"type"`
			Object string          `json:"object"`
			Error  json.RawMessage `json:"error"`
		}
		if errDecode := json.Unmarshal(trimmed, &probe); errDecode == nil {
			switch probe.Type {
			case "response.completed", "response.incomplete":
				return append([]byte(nil), trimmed...), nil
			case "error", "response.failed":
				return nil, codexEventError(trimmed)
			}
			if probe.Object == "response" {
				return wrapCodexResponse(trimmed)
			}
		}
	}

	itemsByIndex := make(map[int]json.RawMessage)
	fallbackItems := make([]json.RawMessage, 0)
	var terminal json.RawMessage
	var streamError error
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), maxCodexEventBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || bytes.Equal(line, []byte("data: [DONE]")) || bytes.Equal(line, []byte("[DONE]")) {
			continue
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			line = bytes.TrimSpace(line[len("data:"):])
		}
		if !json.Valid(line) {
			continue
		}
		var event struct {
			Type        string          `json:"type"`
			OutputIndex json.Number     `json:"output_index"`
			Item        json.RawMessage `json:"item"`
		}
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.UseNumber()
		if errDecode := decoder.Decode(&event); errDecode != nil {
			continue
		}
		switch event.Type {
		case "response.output_item.done":
			if len(event.Item) == 0 || bytes.Equal(event.Item, []byte("null")) {
				continue
			}
			if index, errIndex := strconv.Atoi(event.OutputIndex.String()); errIndex == nil {
				itemsByIndex[index] = append(json.RawMessage(nil), event.Item...)
			} else {
				fallbackItems = append(fallbackItems, append(json.RawMessage(nil), event.Item...))
			}
		case "response.completed", "response.incomplete":
			terminal = append(json.RawMessage(nil), line...)
		case "error", "response.failed":
			streamError = codexEventError(line)
		}
	}
	if errScan := scanner.Err(); errScan != nil {
		return nil, fmt.Errorf("read Mirasim Codex response: %w", errScan)
	}
	if len(terminal) == 0 {
		if streamError != nil {
			return nil, streamError
		}
		return nil, fmt.Errorf("Mirasim Codex response has no terminal event")
	}
	return patchCodexTerminalOutput(terminal, itemsByIndex, fallbackItems)
}

func wrapCodexResponse(response json.RawMessage) ([]byte, error) {
	return json.Marshal(struct {
		Type     string          `json:"type"`
		Response json.RawMessage `json:"response"`
	}{Type: "response.completed", Response: response})
}

func patchCodexTerminalOutput(terminal json.RawMessage, indexed map[int]json.RawMessage, fallback []json.RawMessage) ([]byte, error) {
	if len(indexed) == 0 && len(fallback) == 0 {
		return append([]byte(nil), terminal...), nil
	}
	var event map[string]json.RawMessage
	if errDecode := json.Unmarshal(terminal, &event); errDecode != nil {
		return nil, fmt.Errorf("decode Mirasim Codex terminal event: %w", errDecode)
	}
	var response map[string]json.RawMessage
	if errDecode := json.Unmarshal(event["response"], &response); errDecode != nil {
		return nil, fmt.Errorf("decode Mirasim Codex terminal response: %w", errDecode)
	}
	var existing []json.RawMessage
	_ = json.Unmarshal(response["output"], &existing)
	if len(existing) > 0 {
		return append([]byte(nil), terminal...), nil
	}
	indexes := make([]int, 0, len(indexed))
	for index := range indexed {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	items := make([]json.RawMessage, 0, len(indexes)+len(fallback))
	for _, index := range indexes {
		items = append(items, indexed[index])
	}
	items = append(items, fallback...)
	encodedItems, errItems := json.Marshal(items)
	if errItems != nil {
		return nil, fmt.Errorf("encode Mirasim Codex output items: %w", errItems)
	}
	response["output"] = encodedItems
	encodedResponse, errResponse := json.Marshal(response)
	if errResponse != nil {
		return nil, fmt.Errorf("encode Mirasim Codex terminal response: %w", errResponse)
	}
	event["response"] = encodedResponse
	encodedEvent, errEvent := json.Marshal(event)
	if errEvent != nil {
		return nil, fmt.Errorf("encode Mirasim Codex terminal event: %w", errEvent)
	}
	return encodedEvent, nil
}

func codexEventError(raw []byte) error {
	var event struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
		Response struct {
			Error struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"error"`
		} `json:"response"`
	}
	_ = json.Unmarshal(raw, &event)
	message := strings.TrimSpace(event.Error.Message)
	code := strings.TrimSpace(event.Error.Code)
	if message == "" {
		message = strings.TrimSpace(event.Response.Error.Message)
		code = strings.TrimSpace(event.Response.Error.Code)
	}
	if message == "" {
		message = "upstream stream failed"
	}
	if len(message) > 4096 {
		message = message[:4096] + "..."
	}
	if code != "" {
		return fmt.Errorf("Mirasim Codex stream failed (%s): %s", code, message)
	}
	return fmt.Errorf("Mirasim Codex stream failed: %s", message)
}
