package dialect

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
)

const (
	maxOpenAIImagesMultipartBodyBytes        = 128 << 20
	maxOpenAIImagesMultipartParts            = 128
	maxOpenAIImagesMultipartHeadersPerPart   = 64
	maxOpenAIImagesMultipartHeaderBytes      = 16 << 10
	maxOpenAIImagesMultipartTotalHeaderBytes = 256 << 10
	maxOpenAIImagesContentDispositionBytes   = 4 << 10
	maxOpenAIImagesModelFieldBytes           = 4 << 10
	maxOpenAIImagesStreamFieldBytes          = 16
)

var rewrittenMultipartIntegrityHeaders = []string{
	"Content-Length",
	"Content-Transfer-Encoding",
	"ETag",
	"Digest",
	"Content-MD5",
	"Content-Range",
	"Content-Digest",
	"Repr-Digest",
	"Signature",
	"Signature-Input",
}

type openAIImagesMultipartResult struct {
	model       string
	stream      bool
	body        []byte
	contentType string
}

type openAIImagesMultipartOptions struct {
	replacementModel *string
	forceStream      *bool
	stripControls    bool
}

func processOpenAIImagesMultipart(
	body []byte,
	contentType string,
	replacementModel *string,
) (openAIImagesMultipartResult, error) {
	return processOpenAIImagesMultipartWithOptions(body, contentType, openAIImagesMultipartOptions{
		replacementModel: replacementModel,
	})
}

func processOpenAIImagesMultipartWithOptions(
	body []byte,
	contentType string,
	options openAIImagesMultipartOptions,
) (openAIImagesMultipartResult, error) {
	return processOpenAIImagesMultipartWithOptionsAndLimit(
		body,
		contentType,
		options,
		maxOpenAIImagesMultipartBodyBytes,
	)
}

func processOpenAIImagesMultipartWithOptionsAndLimit(
	body []byte,
	contentType string,
	options openAIImagesMultipartOptions,
	maxBodyBytes int,
) (openAIImagesMultipartResult, error) {
	if maxBodyBytes <= 0 {
		return openAIImagesMultipartResult{}, fmt.Errorf("multipart body limit must be positive")
	}
	if len(body) > maxBodyBytes {
		return openAIImagesMultipartResult{}, fmt.Errorf("multipart body exceeds limit")
	}
	mediaType, parameters, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return openAIImagesMultipartResult{}, fmt.Errorf("decode multipart Content-Type")
	}
	boundary := strings.TrimSpace(parameters["boundary"])
	if boundary == "" {
		return openAIImagesMultipartResult{}, fmt.Errorf("multipart boundary is required")
	}

	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	var output bytes.Buffer
	var writer *multipart.Writer
	if options.replacementModel != nil || options.forceStream != nil || options.stripControls {
		writer = multipart.NewWriter(&output)
	}

	result := openAIImagesMultipartResult{}
	modelSeen := false
	streamSeen := false
	totalHeaderBytes := 0
	partCount := 0
	for {
		part, nextErr := reader.NextRawPart()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return openAIImagesMultipartResult{}, fmt.Errorf("decode multipart part: %w", nextErr)
		}
		partCount++
		if partCount > maxOpenAIImagesMultipartParts {
			return openAIImagesMultipartResult{}, fmt.Errorf("multipart part count exceeds limit")
		}
		if err := validateOpenAIImagesPartHeaders(part.Header, &totalHeaderBytes); err != nil {
			return openAIImagesMultipartResult{}, err
		}
		name, file, err := openAIImagesPartIdentity(part.Header)
		if err != nil {
			return openAIImagesMultipartResult{}, err
		}
		if options.stripControls && openAIImagesSecurityControlName(name) {
			if _, err := io.Copy(io.Discard, part); err != nil {
				return openAIImagesMultipartResult{}, fmt.Errorf("discard multipart control part: %w", err)
			}
			continue
		}

		control := name == "model" || name == "stream"
		var partBody []byte
		if control {
			if file {
				return openAIImagesMultipartResult{}, fmt.Errorf("multipart %s field must not be a file", name)
			}
			limit := maxOpenAIImagesModelFieldBytes
			if name == "stream" {
				limit = maxOpenAIImagesStreamFieldBytes
			}
			partBody, err = readOpenAIImagesControlPart(part, limit)
			if err != nil {
				return openAIImagesMultipartResult{}, fmt.Errorf("decode multipart %s field: %w", name, err)
			}
		}

		switch name {
		case "model":
			if modelSeen {
				return openAIImagesMultipartResult{}, fmt.Errorf("multipart model field must be unique")
			}
			modelSeen = true
			result.model = string(partBody)
			if result.model == "" || strings.TrimSpace(result.model) != result.model {
				return openAIImagesMultipartResult{}, fmt.Errorf("multipart model must be non-empty without boundary whitespace")
			}
		case "stream":
			if streamSeen {
				return openAIImagesMultipartResult{}, fmt.Errorf("multipart stream field must be unique")
			}
			streamSeen = true
			switch string(partBody) {
			case "true":
				result.stream = true
			case "false":
				result.stream = false
			default:
				return openAIImagesMultipartResult{}, fmt.Errorf("multipart stream must be true or false")
			}
		}

		if writer == nil {
			if !control {
				if _, err := io.Copy(io.Discard, part); err != nil {
					return openAIImagesMultipartResult{}, fmt.Errorf("read multipart part: %w", err)
				}
			}
			continue
		}

		header := cloneImagesMIMEHeader(part.Header)
		if name == "model" && options.replacementModel != nil {
			for _, headerName := range rewrittenMultipartIntegrityHeaders {
				header.Del(headerName)
			}
			partBody = []byte(*options.replacementModel)
		}
		if name == "stream" && options.forceStream != nil {
			desired := strconv.FormatBool(*options.forceStream)
			if string(partBody) != desired {
				for _, headerName := range rewrittenMultipartIntegrityHeaders {
					header.Del(headerName)
				}
				partBody = []byte(desired)
			}
		}
		outputPart, err := writer.CreatePart(header)
		if err != nil {
			return openAIImagesMultipartResult{}, fmt.Errorf("rebuild multipart part: %w", err)
		}
		if control {
			if _, err := outputPart.Write(partBody); err != nil {
				return openAIImagesMultipartResult{}, fmt.Errorf("write multipart control part: %w", err)
			}
		} else if _, err := io.Copy(outputPart, part); err != nil {
			return openAIImagesMultipartResult{}, fmt.Errorf("copy multipart part: %w", err)
		}
	}
	if !modelSeen {
		return openAIImagesMultipartResult{}, fmt.Errorf("multipart model field is required")
	}
	if writer != nil {
		if options.forceStream != nil && *options.forceStream && !streamSeen {
			part, err := writer.CreateFormField("stream")
			if err != nil {
				return openAIImagesMultipartResult{}, fmt.Errorf("add multipart stream field: %w", err)
			}
			if _, err := io.WriteString(part, "true"); err != nil {
				return openAIImagesMultipartResult{}, fmt.Errorf("write multipart stream field: %w", err)
			}
		}
		if err := writer.Close(); err != nil {
			return openAIImagesMultipartResult{}, fmt.Errorf("close rebuilt multipart body: %w", err)
		}
		if output.Len() > maxBodyBytes {
			return openAIImagesMultipartResult{}, fmt.Errorf("rebuilt multipart body exceeds limit")
		}
		result.body = output.Bytes()
		result.contentType = writer.FormDataContentType()
	}
	return result, nil
}

func openAIImagesSecurityControlName(name string) bool {
	normalized := strings.ReplaceAll(strings.ToLower(name), "-", "_")
	switch normalized {
	case "provider", "fallback", "fallbacks", "authorization", "proxy_authorization",
		"api_key", "apikey", "x_api_key", "x_goog_api_key":
		return true
	default:
		return false
	}
}

func validateOpenAIImagesPartHeaders(header textproto.MIMEHeader, totalBytes *int) error {
	count := 0
	partBytes := 0
	for name, values := range header {
		count += len(values)
		for _, value := range values {
			partBytes += len(name) + len(value) + 4
		}
	}
	if count > maxOpenAIImagesMultipartHeadersPerPart {
		return fmt.Errorf("multipart part header count exceeds limit")
	}
	if partBytes > maxOpenAIImagesMultipartHeaderBytes {
		return fmt.Errorf("multipart part header bytes exceed limit")
	}
	*totalBytes += partBytes
	if *totalBytes > maxOpenAIImagesMultipartTotalHeaderBytes {
		return fmt.Errorf("multipart total header bytes exceed limit")
	}
	dispositionValues := header.Values("Content-Disposition")
	if len(dispositionValues) != 1 || len(dispositionValues[0]) > maxOpenAIImagesContentDispositionBytes {
		return fmt.Errorf("multipart Content-Disposition is missing, repeated, or exceeds limit")
	}
	return nil
}

func openAIImagesPartIdentity(header textproto.MIMEHeader) (string, bool, error) {
	disposition, parameters, err := mime.ParseMediaType(header.Get("Content-Disposition"))
	if err != nil || !strings.EqualFold(disposition, "form-data") {
		return "", false, fmt.Errorf("multipart Content-Disposition is invalid")
	}
	name := parameters["name"]
	if name == "" {
		return "", false, fmt.Errorf("multipart part name is required")
	}
	_, hasFilename := parameters["filename"]
	return name, hasFilename, nil
}

func readOpenAIImagesControlPart(part *multipart.Part, limit int) ([]byte, error) {
	reader := io.LimitReader(part, int64(limit)+1)
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if len(body) > limit {
		return nil, fmt.Errorf("field exceeds %d bytes", limit)
	}
	return body, nil
}

func cloneImagesMIMEHeader(source textproto.MIMEHeader) textproto.MIMEHeader {
	return textproto.MIMEHeader(http.Header(source).Clone())
}

func applyRebuiltImagesMultipart(request *ParsedRequest, result openAIImagesMultipartResult) (*ParsedRequest, error) {
	if request == nil {
		return nil, fmt.Errorf("parsed request is required")
	}
	clone := *request
	clone.Header = request.Header.Clone()
	clone.Body = result.body
	if clone.Header == nil {
		clone.Header = make(http.Header)
	}
	for _, headerName := range rewrittenMultipartIntegrityHeaders {
		clone.Header.Del(headerName)
	}
	clone.Header.Set("Content-Type", result.contentType)
	clone.Header.Set("Content-Length", strconv.Itoa(len(clone.Body)))
	return &clone, nil
}
