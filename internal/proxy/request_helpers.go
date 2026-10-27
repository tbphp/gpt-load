package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/models"
	"gpt-load/internal/utils"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/sirupsen/logrus"
)

// maxDecodedErrorBodyBytes bounds an error body after decompression: only a short
// message is kept, while a compressed body can expand by orders of magnitude.
const maxDecodedErrorBodyBytes = 1 << 20

func (ps *ProxyServer) applyParamOverrides(bodyBytes []byte, group *models.Group) ([]byte, error) {
	if len(group.ParamOverrides) == 0 || len(bodyBytes) == 0 {
		return bodyBytes, nil
	}

	var requestData map[string]any
	if err := json.Unmarshal(bodyBytes, &requestData); err != nil {
		logrus.Warnf("failed to unmarshal request body for param override, passing through: %v", err)
		return bodyBytes, nil
	}

	for key, value := range group.ParamOverrides {
		requestData[key] = value
	}

	return json.Marshal(requestData)
}

// logUpstreamError provides a centralized way to log errors from upstream interactions.
func logUpstreamError(context string, err error) {
	if err == nil {
		return
	}
	if app_errors.IsIgnorableError(err) {
		logrus.Debugf("Ignorable upstream error in %s: %v", context, err)
	} else {
		logrus.Errorf("Upstream error in %s: %v", context, err)
	}
}

// decodeUpstreamErrorBody returns a readable version of an upstream error body.
//
// The caller's Accept-Encoding header is forwarded, so an upstream may compress
// the body with any coding the caller advertised; Go's transport only decodes
// gzip that it negotiated itself. Undecodable bodies are replaced with a
// diagnostic, because compressed bytes read as mojibake.
func decodeUpstreamErrorBody(resp *http.Response, bodyBytes []byte) []byte {
	// Repeated field lines are equivalent to one comma separated list (RFC 9110 5.3).
	contentEncoding := strings.Join(resp.Header.Values("Content-Encoding"), ",")

	decompressed, err := utils.DecompressResponseLimited(contentEncoding, bodyBytes, maxDecodedErrorBodyBytes)
	if err == nil {
		return decompressed
	}

	logrus.WithError(err).Warnf("Failed to decompress upstream error body (Content-Encoding: %q)", contentEncoding)
	if errors.Is(err, utils.ErrDecodedBodyTooLarge) {
		return []byte(fmt.Sprintf("[upstream error body too large, Content-Encoding: %s]", contentEncoding))
	}

	// Some upstreams label an already decoded body, which is still readable.
	if utf8.Valid(bodyBytes) {
		return bodyBytes
	}
	return []byte(fmt.Sprintf("[undecodable upstream error body, Content-Encoding: %s]", contentEncoding))
}
