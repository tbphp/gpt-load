package codexrecovery

import "testing"

func TestClassifyRecognizesEncryptedContentRejections(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  Signal
	}{
		{
			// The production rejection that motivated this package.
			name:  "verified_failure_plain_message",
			input: "The encrypted content for item rs_00113ff53ff1b365016ad0b0aff80487d1bf2351cfe77bb2df could not be verified. Reason: Encrypted content could not be decrypted or parsed. [trace_id=90789c337c36f500b7e2cf30ab7faca3]",
			want:  SignalEncryptedContentDecryptionFailed,
		},
		{
			name:  "exact_code",
			input: "encrypted_content_decryption_failed",
			want:  SignalEncryptedContentDecryptionFailed,
		},
		{
			name:  "encrypted_context_invalid_code",
			input: "encrypted_context_invalid",
			want:  SignalEncryptedContextInvalid,
		},
		{
			name:  "invalid_encrypted_content_code",
			input: "invalid_encrypted_content",
			want:  SignalInvalidEncryptedContent,
		},
		{
			name:  "could_not_validate",
			input: `{"error":{"message":"The upstream could not validate encrypted continuation context"}}`,
			want:  SignalEncryptedContentDecryptionFailed,
		},
		{
			name:  "nested_error_code",
			input: `{"error":{"code":"invalid_encrypted_content","message":"nope"}}`,
			want:  SignalInvalidEncryptedContent,
		},
		{
			name:  "responses_error_shape",
			input: `{"response":{"error":{"code":"encrypted_context_invalid","message":"bad context"}}}`,
			want:  SignalEncryptedContextInvalid,
		},
		{
			name:  "sse_framed_failure",
			input: "event: response.failed\ndata: {\"response\":{\"error\":{\"message\":\"encrypted content could not be parsed\"}}}",
			want:  SignalEncryptedContentDecryptionFailed,
		},
		{
			name:  "unrelated_error",
			input: "invalid_api_key: the provided key is not valid",
			want:  "",
		},
		{
			name:  "encrypted_without_decrypt_marker",
			input: "encrypted_content field is present",
			want:  "",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := Classify(testCase.input); got != testCase.want {
				t.Fatalf("Classify(%q) = %q, want %q", testCase.input, got, testCase.want)
			}
		})
	}
}

func TestClassifyPartsPrefersExactCode(t *testing.T) {
	if got := ClassifyParts("encrypted_context_invalid", ""); got != SignalEncryptedContextInvalid {
		t.Fatalf("exact code = %q, want %q", got, SignalEncryptedContextInvalid)
	}
	if got := ClassifyParts("", "Encrypted content could not be verified"); got != SignalEncryptedContentDecryptionFailed {
		t.Fatalf("message heuristic = %q, want %q", got, SignalEncryptedContentDecryptionFailed)
	}
	if got := ClassifyParts("rate_limit_exceeded", "too many requests"); got != "" {
		t.Fatalf("unrelated parts = %q, want empty", got)
	}
}

func TestInvalidEncryptedContentSignalRecognizesOnlyOpaqueStateRejections(t *testing.T) {
	for _, signal := range []Signal{
		SignalThinkingSignatureInvalid,
		SignalInvalidEncryptedContent,
		SignalEncryptedContentDecryptionFailed,
		SignalEncryptedContextInvalid,
	} {
		if !InvalidEncryptedContentSignal(signal) {
			t.Fatalf("encrypted-content signal %q was ignored", signal)
		}
	}
	for _, signal := range []Signal{SignalItemResourceMismatch, SignalItemNotFound, ""} {
		if InvalidEncryptedContentSignal(signal) {
			t.Fatalf("non-encrypted signal %q was remembered by digest", signal)
		}
	}
}

func TestClassifyRecognizesResponseItemScopeErrors(t *testing.T) {
	message := "OpenAI Responses bad request: The requested item was created under a different Azure OpenAI resource. Use the same resource that created the item to access it."
	for _, value := range []string{
		message,
		`{"error":{"code":"invalid_request_error","message":"` + message + `"}}`,
		"event: response.failed\ndata: {\"response\":{\"error\":{\"message\":\"" + message + "\"}}}\n\n",
	} {
		if signal := Classify(value); signal == "" {
			t.Fatalf("item resource mismatch was not classified: %s", value)
		}
	}
	for _, value := range []string{
		"Item with id 'rs_previous' not found. Items are not persisted when store is set to false.",
		`{"error":{"message":"No item found with id 'msg_previous'."}}`,
	} {
		if signal := Classify(value); signal == "" {
			t.Fatalf("missing history item was not classified: %s", value)
		}
	}
	for _, value := range []string{
		"invalid_request: please check the request parameters",
		"File with id 'file_missing' not found",
		"The model belongs to a different project",
		`{"output":[{"type":"message","content":"encrypted content could not be verified"}]}`,
	} {
		if signal := Classify(value); signal != "" {
			t.Fatalf("unrelated payload was classified as %q: %s", signal, value)
		}
	}
	if signal := ClassifyParts("invalid_request", message); signal == "" {
		t.Fatal("decoded item scope error was not classified")
	}
}
