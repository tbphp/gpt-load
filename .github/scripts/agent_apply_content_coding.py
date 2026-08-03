from pathlib import Path


def replace_exact(path: str, old: str, new: str) -> None:
    file_path = Path(path)
    text = file_path.read_text()
    count = text.count(old)
    if count != 1:
        raise RuntimeError(f"{path}: expected one match, found {count}: {old[:80]!r}")
    file_path.write_text(text.replace(old, new, 1))


replace_exact(
    "internal/gateway/handler.go",
    '"gpt-load/internal/platform/encryption"\n\t"gpt-load/internal/platform/utils"',
    '"gpt-load/internal/platform/contentcoding"\n\t"gpt-load/internal/platform/encryption"\n\t"gpt-load/internal/platform/utils"',
)

replace_exact(
    "internal/gateway/handler.go",
    '''\tif selectedRoute.Kind == endpointModels {
\t\thandler.writeVisibleModelList(ginContext, snapshot, accessKey, selectedRoute.Protocol)
\t\treturn
\t}
''',
    '''\tif !contentcoding.AcceptsIdentity(ginContext.Request.Header.Values("Accept-Encoding")) {
\t\thandler.completeReason(ginContext, recorder, reasonNotAcceptable)
\t\treturn
\t}
\tif selectedRoute.Kind == endpointModels {
\t\thandler.writeVisibleModelList(ginContext, snapshot, accessKey, selectedRoute.Protocol)
\t\treturn
\t}
''',
)

replace_exact(
    "internal/gateway/handler.go",
    '''\tbody, err := readRequestBody(ginContext.Request.Body, maxRequestBodyBytes)
\tif err != nil {
\t\tif ginContext.Request.Context().Err() != nil {
\t\t\trecorder.completeCanceled(0)
\t\t\treturn
\t\t}
\t\tif errors.Is(err, errRequestTooLarge) {
\t\t\thandler.completeReason(ginContext, recorder, reasonRequestTooLarge)
\t\t\treturn
\t\t}
\t\thandler.completeReason(ginContext, recorder, reasonInvalidProtocolRequest)
\t\treturn
\t}
\tparsed := &dialect.ParsedRequest{
\t\tMethod:   ginContext.Request.Method,
\t\tPath:     ginContext.Request.URL.Path,
\t\tRawQuery: ginContext.Request.URL.RawQuery,
\t\tHeader:   ginContext.Request.Header.Clone(),
\t\tBody:     body,
\t}
''',
    '''\tbody, requestHeaders, err := readDecodedRequestBody(
\t\tginContext.Request,
\t\tmaxRequestBodyBytes,
\t\tmaxRequestBodyBytes,
\t)
\tif err != nil {
\t\tif ginContext.Request.Context().Err() != nil {
\t\t\trecorder.completeCanceled(0)
\t\t\treturn
\t\t}
\t\tswitch {
\t\tcase errors.Is(err, contentcoding.ErrEncodedTooLarge),
\t\t\terrors.Is(err, contentcoding.ErrDecodedTooLarge),
\t\t\terrors.Is(err, errRequestTooLarge):
\t\t\thandler.completeReason(ginContext, recorder, reasonRequestTooLarge)
\t\tcase errors.Is(err, contentcoding.ErrUnsupportedEncoding):
\t\t\thandler.completeReason(ginContext, recorder, reasonUnsupportedContentEncoding)
\t\tcase errors.Is(err, contentcoding.ErrInvalidEncoding):
\t\t\thandler.completeReason(ginContext, recorder, reasonInvalidContentEncoding)
\t\tdefault:
\t\t\thandler.completeReason(ginContext, recorder, reasonInvalidProtocolRequest)
\t\t}
\t\treturn
\t}
\tparsed := &dialect.ParsedRequest{
\t\tMethod:   ginContext.Request.Method,
\t\tPath:     ginContext.Request.URL.Path,
\t\tRawQuery: ginContext.Request.URL.RawQuery,
\t\tHeader:   requestHeaders,
\t\tBody:     body,
\t}
''',
)

replace_exact(
    "internal/gateway/forward.go",
    '''\t})
\tinvalidateRewrittenBodyHeaders(headers)

\tfirstEvent, err := bufferFirstSSEEvent(streamBody)
''',
    '''\t})
\tnormalizePlainStreamingResponseHeaders(headers)

\tfirstEvent, err := bufferFirstSSEEvent(streamBody)
''',
)

replace_exact(
    "internal/gateway/forward.go",
    '''\tbodyChanged := !bytes.Equal(input.Request.Body, parsed.Body)
\tupstreamURL, err := input.Dialect.BuildUpstreamURL(input.Group.UpstreamURL, parsed)
''',
    '''\tupstreamURL, err := input.Dialect.BuildUpstreamURL(input.Group.UpstreamURL, parsed)
''',
)

replace_exact(
    "internal/gateway/forward.go",
    '''\trequest.Header = cloneEndToEndHeaders(parsed.Header)
\tif bodyChanged {
\t\tinvalidateRewrittenBodyHeaders(request.Header)
\t}
\tremoveDownstreamCredentials(request.Header)
\tdialect.ApplyCredential(input.Dialect, request.Header, input.APIKey, input.Group.HeaderRules)
\tsanitizeUpstreamRequestHeaders(request.Header)
\tif stream || rewrite {
\t\trequest.Header.Set("Accept-Encoding", "identity")
\t}
''',
    '''\trequest.Header = cloneEndToEndHeaders(parsed.Header)
\tstripRepresentationMetadata(request.Header)
\tremoveDownstreamCredentials(request.Header)
\tdialect.ApplyCredential(input.Dialect, request.Header, input.APIKey, input.Group.HeaderRules)
\tsanitizeUpstreamRequestHeaders(request.Header)
\tstripRepresentationMetadata(request.Header)
\trequest.Header.Set("Accept-Encoding", "identity")
''',
)

replace_exact(
    "internal/gateway/forward.go",
    '''\tif bytes.Equal(downstreamPlain, plain) {
\t\treturn bytes.Clone(wire), safePlain
\t}
\tsafeWire, err := utils.CompressResponse(encoding, downstreamPlain)
\tif err != nil || int64(len(safeWire)) > maxErrorResponseBodyBytes {
\t\tif needsModelRewrite(input) {
\t\t\twire, _ := failClosedErrorBody(headers)
\t\t\treturn wire, safePlain
\t\t}
\t\treturn failClosedErrorBody(headers)
\t}
\tupdateRewrittenBodyHeaders(headers, len(safeWire))
\treturn safeWire, safePlain
''',
    '''\tdownstreamBody := bytes.Clone(downstreamPlain)
\tif int64(len(downstreamBody)) > maxDecompressedErrorBodyBytes {
\t\treturn failClosedErrorBody(headers)
\t}
\trebuildPlainBufferedResponseHeaders(headers, len(downstreamBody))
\treturn downstreamBody, safePlain
''',
)

replace_exact(
    "internal/gateway/response_representation.go",
    '''\tpreparedWire := bytes.Clone(wire)
\tif changed {
\t\tpreparedWire, err = utils.CompressResponse(encoding, downstreamPlain)
\t\tif err != nil {
\t\t\treturn preparedSuccessRepresentation{}, successRepresentationProtocolError("recompress response body")
\t\t}
\t\tif int64(len(preparedWire)) > maxNonStreamingResponseBodyBytes {
\t\t\treturn preparedSuccessRepresentation{}, successRepresentationProtocolError("recompressed response body exceeds limit")
\t\t}
\t\tupdateRewrittenBodyHeaders(preparedHeaders, len(preparedWire))
\t}
''',
    '''\tpreparedWire := bytes.Clone(downstreamPlain)
\tif int64(len(preparedWire)) > maxNonStreamingResponseBodyBytes {
\t\treturn preparedSuccessRepresentation{}, successRepresentationProtocolError("downstream response body exceeds limit")
\t}
\trebuildPlainBufferedResponseHeaders(preparedHeaders, len(preparedWire))
''',
)

replace_exact(
    "internal/platform/httpheader/policy_test.go",
    '''\t\t\t\tt.Errorf(
\t\t\t\t\t"IsForbiddenRequestRuleSetName(%q) = %t, want %t",
\t\t\t\t\tgot,
\t\t\t\t\ttest.name,
\t\t\t\t\ttest.want,
\t\t\t\t)
''',
    '''\t\t\t\tt.Errorf(
\t\t\t\t\t"IsForbiddenRequestRuleSetName(%q) = %t, want %t",
\t\t\t\t\ttest.name,
\t\t\t\t\tgot,
\t\t\t\t\ttest.want,
\t\t\t\t)
''',
)
