package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/tidwall/gjson"

	"gpt-load/internal/protocol"
)

const (
	maxUnaryRestoreDepth   = 64
	maxUnaryRestorePatches = 65536
	maxUnaryRestoreBytes   = 128 << 20
)

var errUnaryRestore = errors.New("cannot restore response content")

type unaryRestorePatch struct {
	start int
	end   int
	value []byte
}

type unaryRestoreContext struct {
	body       []byte
	restore    func(string) (string, error)
	structured bool
	patches    []unaryRestorePatch
}

// restoreUnaryBusinessFields restores only protocol business fields. It keeps
// unchanged JSON bytes, including field order and unrelated escape sequences.
func restoreUnaryBusinessFields(
	body []byte,
	clientProtocol protocol.Protocol,
	restore func(string) (string, error),
	structuredOutput bool,
) ([]byte, error) {
	if restore == nil {
		return body, nil
	}
	switch clientProtocol {
	case protocol.OpenAICompletions, protocol.OpenAIResponses, protocol.Anthropic, protocol.Gemini:
	default:
		return body, nil
	}
	if !bytes.Contains(body, []byte("gld1_")) && !bytes.Contains(body, []byte(`\u`)) {
		return body, nil
	}
	if len(body) > maxUnaryRestoreBytes || !json.Valid(body) || !utf8.Valid(body) {
		return nil, errUnaryRestore
	}
	ctx := unaryRestoreContext{body: body, restore: restore, structured: structuredOutput}
	root := gjson.ParseBytes(body)
	var err error
	switch clientProtocol {
	case protocol.OpenAICompletions:
		err = ctx.chat(root)
	case protocol.OpenAIResponses:
		err = ctx.responses(root, 0)
	case protocol.Anthropic:
		err = ctx.anthropic(root)
	case protocol.Gemini:
		err = ctx.gemini(root)
	}
	if err != nil {
		return nil, errUnaryRestore
	}
	return ctx.apply()
}

func unaryRestoreField(object gjson.Result, name string, visit func(gjson.Result) error) error {
	if !object.IsObject() {
		return nil
	}
	var err error
	object.ForEach(func(key, value gjson.Result) bool {
		if key.Str == name {
			err = visit(value)
		}
		return err == nil
	})
	return err
}

func unaryRestoreArray(array gjson.Result, visit func(gjson.Result) error) error {
	if !array.IsArray() {
		return nil
	}
	var err error
	array.ForEach(func(_, value gjson.Result) bool {
		err = visit(value)
		return err == nil
	})
	return err
}

func (ctx *unaryRestoreContext) chat(root gjson.Result) error {
	return unaryRestoreField(root, "choices", func(choices gjson.Result) error {
		return unaryRestoreArray(choices, func(choice gjson.Result) error {
			return unaryRestoreField(choice, "message", ctx.chatMessage)
		})
	})
}

func (ctx *unaryRestoreContext) chatMessage(message gjson.Result) error {
	if err := unaryRestoreField(message, "content", func(content gjson.Result) error {
		if content.Type == gjson.String {
			return ctx.text(content)
		}
		return unaryRestoreArray(content, func(part gjson.Result) error {
			if part.Get("type").Str != "text" {
				return nil
			}
			return unaryRestoreField(part, "text", ctx.text)
		})
	}); err != nil {
		return err
	}
	if err := unaryRestoreField(message, "function_call", ctx.arguments); err != nil {
		return err
	}
	return unaryRestoreField(message, "tool_calls", func(calls gjson.Result) error {
		return unaryRestoreArray(calls, func(call gjson.Result) error {
			if err := unaryRestoreField(call, "function", ctx.arguments); err != nil {
				return err
			}
			return unaryRestoreField(call, "custom", ctx.customToolInput)
		})
	})
}

func (ctx *unaryRestoreContext) responses(root gjson.Result, depth int) error {
	if depth > maxUnaryRestoreDepth {
		return errUnaryRestore
	}
	if root.Get("object").Str == "list" {
		return ctx.responsesInputItems(root)
	}
	if err := unaryRestoreField(root, "response", func(envelope gjson.Result) error {
		return ctx.responses(envelope, depth+1)
	}); err != nil {
		return err
	}
	if err := unaryRestoreField(root, "output_text", ctx.text); err != nil {
		return err
	}
	return unaryRestoreField(root, "output", func(output gjson.Result) error {
		return unaryRestoreArray(output, func(item gjson.Result) error {
			switch item.Get("type").Str {
			case "message":
				return unaryRestoreField(item, "content", func(content gjson.Result) error {
					return unaryRestoreArray(content, func(part gjson.Result) error {
						if part.Get("type").Str != "output_text" {
							return nil
						}
						return unaryRestoreField(part, "text", ctx.text)
					})
				})
			case "function_call":
				return ctx.arguments(item)
			case "custom_tool_call":
				return ctx.customToolInput(item)
			default:
				return nil
			}
		})
	})
}

func (ctx *unaryRestoreContext) responsesInputItems(root gjson.Result) error {
	return unaryRestoreField(root, "data", func(data gjson.Result) error {
		return unaryRestoreArray(data, func(item gjson.Result) error {
			switch item.Get("type").Str {
			case "message":
				return unaryRestoreField(item, "content", func(content gjson.Result) error {
					if content.Type == gjson.String {
						return ctx.text(content)
					}
					return unaryRestoreArray(content, func(part gjson.Result) error {
						switch part.Get("type").Str {
						case "input_text", "output_text":
							return unaryRestoreField(part, "text", ctx.text)
						default:
							return nil
						}
					})
				})
			case "function_call":
				return ctx.arguments(item)
			case "custom_tool_call":
				return ctx.customToolInput(item)
			case "function_call_output", "custom_tool_call_output":
				return unaryRestoreField(item, "output", func(output gjson.Result) error {
					if output.Type == gjson.String {
						return ctx.jsonValue(output)
					}
					return unaryRestoreArray(output, func(part gjson.Result) error {
						if part.Get("type").Str == "input_text" || part.Get("type").Str == "output_text" {
							return unaryRestoreField(part, "text", ctx.jsonValue)
						}
						return nil
					})
				})
			default:
				return nil
			}
		})
	})
}

func (ctx *unaryRestoreContext) anthropic(root gjson.Result) error {
	return unaryRestoreField(root, "content", func(content gjson.Result) error {
		return unaryRestoreArray(content, func(block gjson.Result) error {
			switch block.Get("type").Str {
			case "text":
				return unaryRestoreField(block, "text", ctx.text)
			case "tool_use":
				return unaryRestoreField(block, "input", ctx.jsonValue)
			default:
				return nil
			}
		})
	})
}

func (ctx *unaryRestoreContext) gemini(root gjson.Result) error {
	return unaryRestoreField(root, "candidates", func(candidates gjson.Result) error {
		return unaryRestoreArray(candidates, func(candidate gjson.Result) error {
			return unaryRestoreField(candidate, "content", func(content gjson.Result) error {
				return unaryRestoreField(content, "parts", func(parts gjson.Result) error {
					return unaryRestoreArray(parts, func(part gjson.Result) error {
						if part.Get("thought").Bool() {
							return nil
						}
						before := len(ctx.patches)
						if err := unaryRestoreField(part, "text", ctx.text); err != nil {
							return err
						}
						if err := unaryRestoreField(part, "functionCall", func(call gjson.Result) error {
							return unaryRestoreField(call, "args", ctx.jsonValue)
						}); err != nil {
							return err
						}
						if len(ctx.patches) > before && part.Get("thoughtSignature").Exists() {
							return errUnaryRestore
						}
						return nil
					})
				})
			})
		})
	})
}

func (ctx *unaryRestoreContext) customToolInput(item gjson.Result) error {
	return unaryRestoreField(item, "input", func(value gjson.Result) error {
		return ctx.restoreString(value, ctx.addPatch)
	})
}

func (ctx *unaryRestoreContext) arguments(call gjson.Result) error {
	return unaryRestoreField(call, "arguments", ctx.jsonDocument)
}

func (ctx *unaryRestoreContext) text(value gjson.Result) error {
	if value.Type != gjson.String {
		return nil
	}
	if ctx.structured {
		return ctx.embeddedJSON(value, true)
	}
	return ctx.restoreString(value, ctx.addPatch)
}

// jsonDocument 用于明确为 JSON 的工具参数；普通工具返回文本仍走 jsonValue。
func (ctx *unaryRestoreContext) jsonDocument(value gjson.Result) error {
	if value.Type == gjson.String {
		return ctx.embeddedJSON(value, true)
	}
	return ctx.walkJSONValues(value, 0, ctx.addPatch)
}

func (ctx *unaryRestoreContext) jsonValue(value gjson.Result) error {
	if value.Type == gjson.String {
		return ctx.embeddedJSON(value, false)
	}
	return ctx.walkJSONValues(value, 0, ctx.addPatch)
}

func (ctx *unaryRestoreContext) embeddedJSON(value gjson.Result, required bool) error {
	inner := []byte(value.Str)
	if !json.Valid(inner) || !utf8.Valid(inner) {
		if !required {
			return ctx.restoreString(value, ctx.addPatch)
		}
		if !strings.Contains(value.Str, redactionStreamPrefix) && !strings.Contains(value.Str, `\u`) {
			return nil
		}
		// 与 delta 共用字符串/转义边界，普通 JSON 截断不等于密文截断。
		doc := redactionStreamDocument{}
		prefix, err := doc.push(value.Str, ctx.restore)
		if err != nil {
			return errUnaryRestore
		}
		tail, err := doc.finish(ctx.restore)
		if err != nil || len(prefix) > maxUnaryRestoreBytes-len(tail) {
			return errUnaryRestore
		}
		restored := prefix + tail
		if restored == value.Str {
			return nil
		}
		encoded, err := json.Marshal(restored)
		if err != nil {
			return errUnaryRestore
		}
		return ctx.addPatch(unaryRestorePatch{value.Index, value.Index + len(value.Raw), encoded})
	}
	var innerPatches []unaryRestorePatch
	collect := func(p unaryRestorePatch) error {
		if len(ctx.patches)+len(innerPatches) >= maxUnaryRestorePatches {
			return errUnaryRestore
		}
		innerPatches = append(innerPatches, p)
		return nil
	}
	if err := ctx.walkJSONValues(gjson.ParseBytes(inner), 0, collect); err != nil {
		return err
	}
	if len(innerPatches) == 0 {
		return nil
	}
	boundaries, err := unaryJSONDecodedBoundaries(value.Raw, value.Str)
	if err != nil {
		return err
	}
	for _, patch := range innerPatches {
		if patch.start < 0 || patch.end > len(inner) || patch.start >= patch.end ||
			boundaries[patch.start] < 0 || boundaries[patch.end] < 0 {
			return errUnaryRestore
		}
		encoded, err := json.Marshal(string(patch.value))
		if err != nil {
			return errUnaryRestore
		}
		if err := ctx.addPatch(unaryRestorePatch{
			start: value.Index + boundaries[patch.start],
			end:   value.Index + boundaries[patch.end],
			value: encoded[1 : len(encoded)-1],
		}); err != nil {
			return err
		}
	}
	return nil
}

func (ctx *unaryRestoreContext) walkJSONValues(
	value gjson.Result,
	depth int,
	add func(unaryRestorePatch) error,
) error {
	if depth > maxUnaryRestoreDepth {
		return errUnaryRestore
	}
	if value.Type == gjson.String {
		return ctx.restoreString(value, add)
	}
	if !value.IsObject() && !value.IsArray() {
		return nil
	}
	var err error
	value.ForEach(func(_, child gjson.Result) bool {
		err = ctx.walkJSONValues(child, depth+1, add)
		return err == nil
	})
	return err
}

func (ctx *unaryRestoreContext) restoreString(
	value gjson.Result,
	add func(unaryRestorePatch) error,
) error {
	if value.Type != gjson.String {
		return nil
	}
	restored, err := ctx.restore(value.Str)
	if err != nil || !utf8.ValidString(restored) {
		return errUnaryRestore
	}
	if restored == value.Str {
		return nil
	}
	encoded, err := json.Marshal(restored)
	if err != nil {
		return errUnaryRestore
	}
	return add(unaryRestorePatch{value.Index, value.Index + len(value.Raw), encoded})
}

func (ctx *unaryRestoreContext) addPatch(p unaryRestorePatch) error {
	if len(ctx.patches) >= maxUnaryRestorePatches {
		return errUnaryRestore
	}
	ctx.patches = append(ctx.patches, p)
	return nil
}

func (ctx *unaryRestoreContext) apply() ([]byte, error) {
	if len(ctx.patches) == 0 {
		return ctx.body, nil
	}
	sort.Slice(ctx.patches, func(i, j int) bool { return ctx.patches[i].start < ctx.patches[j].start })
	var result bytes.Buffer
	result.Grow(len(ctx.body))
	position := 0
	for _, patch := range ctx.patches {
		if patch.start < position || patch.end > len(ctx.body) || patch.start > patch.end ||
			result.Len()+patch.start-position+len(patch.value) > maxUnaryRestoreBytes {
			return nil, errUnaryRestore
		}
		result.Write(ctx.body[position:patch.start])
		result.Write(patch.value)
		position = patch.end
	}
	if result.Len()+len(ctx.body)-position > maxUnaryRestoreBytes {
		return nil, errUnaryRestore
	}
	result.Write(ctx.body[position:])
	return result.Bytes(), nil
}

// unaryJSONDecodedBoundaries maps decoded bytes to offsets in their outer JSON
// string literal so that inner JSON edits preserve all untouched outer bytes.
func unaryJSONDecodedBoundaries(raw, decoded string) ([]int, error) {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return nil, errUnaryRestore
	}
	boundaries := make([]int, len(decoded)+1)
	for i := range boundaries {
		boundaries[i] = -1
	}
	boundaries[0] = 1
	var actual []byte
	appendBytes := func(start, end int, chunk []byte) error {
		at := len(actual)
		if at+len(chunk) > len(decoded) {
			return errUnaryRestore
		}
		boundaries[at] = start
		actual = append(actual, chunk...)
		boundaries[len(actual)] = end
		return nil
	}
	for i := 1; i < len(raw)-1; {
		start := i
		if raw[i] != '\\' {
			if err := appendBytes(start, i+1, []byte{raw[i]}); err != nil {
				return nil, err
			}
			i++
			continue
		}
		if i+1 >= len(raw)-1 {
			return nil, errUnaryRestore
		}
		var chunk []byte
		switch raw[i+1] {
		case '"', '\\', '/':
			chunk = []byte{raw[i+1]}
			i += 2
		case 'b', 'f', 'n', 'r', 't':
			switch raw[i+1] {
			case 'b':
				chunk = []byte{'\b'}
			case 'f':
				chunk = []byte{'\f'}
			case 'n':
				chunk = []byte{'\n'}
			case 'r':
				chunk = []byte{'\r'}
			case 't':
				chunk = []byte{'\t'}
			}
			i += 2
		case 'u':
			if i+6 > len(raw)-1 {
				return nil, errUnaryRestore
			}
			code, err := strconv.ParseUint(raw[i+2:i+6], 16, 16)
			if err != nil {
				return nil, errUnaryRestore
			}
			i += 6
			r := rune(code)
			if utf16.IsSurrogate(r) {
				if r >= 0xD800 && r <= 0xDBFF && i+6 <= len(raw)-1 &&
					raw[i:i+2] == "\\u" {
					second, err := strconv.ParseUint(raw[i+2:i+6], 16, 16)
					if err == nil && rune(second) >= 0xDC00 && rune(second) <= 0xDFFF {
						r = utf16.DecodeRune(r, rune(second))
						i += 6
					} else {
						r = utf8.RuneError
					}
				} else {
					r = utf8.RuneError
				}
			}
			chunk = utf8.AppendRune(nil, r)
		default:
			return nil, errUnaryRestore
		}
		if err := appendBytes(start, i, chunk); err != nil {
			return nil, err
		}
	}
	if !bytes.Equal(actual, []byte(decoded)) || boundaries[len(decoded)] != len(raw)-1 {
		return nil, errUnaryRestore
	}
	return boundaries, nil
}
