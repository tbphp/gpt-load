package webui

import (
	"strings"
	"testing"
)

func TestReleaseWorkflowParallelizesIndependentBuildStages(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	webJob := workflowJobBlock(t, content, "verify-and-build-web")
	staticJob := workflowJobBlock(t, content, "static-checks")
	for _, command := range []string{"govulncheck", "go vet ./...", "go build -trimpath"} {
		if strings.Contains(webJob, command) {
			t.Fatalf("web artifact job still serializes %q:\n%s", command, webJob)
		}
		if !strings.Contains(staticJob, command) {
			t.Fatalf("parallel static job does not contain %q:\n%s", command, staticJob)
		}
	}

	binaryJob := workflowJobBlock(t, content, "build-binaries")
	for _, required := range []string{"verify-and-build-web", "max-parallel: 5"} {
		if !strings.Contains(binaryJob, required) {
			t.Fatalf("binary build job does not contain %q:\n%s", required, binaryJob)
		}
	}

	dockerSmoke := workflowJobBlock(t, content, "docker-smoke")
	if !strings.Contains(dockerSmoke, "validate-tag") || strings.Contains(dockerSmoke, "verify-and-build-web") {
		t.Fatalf("Docker smoke is not released directly after tag validation:\n%s", dockerSmoke)
	}

	metadataJob := workflowJobBlock(t, content, "package-metadata")
	for _, required := range []string{"validate-tag", "name: release-metadata", "bom.cdx.json"} {
		if !strings.Contains(metadataJob, required) {
			t.Fatalf("release metadata job does not contain %q:\n%s", required, metadataJob)
		}
	}
	checksumJob := workflowJobBlock(t, content, "package-checksums")
	for _, required := range []string{"build-binaries", "package-metadata", "name: release-metadata"} {
		if !strings.Contains(checksumJob, required) {
			t.Fatalf("checksum job does not contain %q:\n%s", required, checksumJob)
		}
	}

	preflight := workflowJobBlock(t, content, "publication-preflight")
	for _, dependency := range []string{"static-checks", "race-cpa"} {
		if !strings.Contains(preflight, dependency) {
			t.Fatalf("publication preflight does not need %s:\n%s", dependency, preflight)
		}
	}
}

func TestReleaseWorkflowRunsPublishedImageSmokesInParallel(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	smokeJob := workflowJobBlock(t, content, "post-publish-image-smoke")
	for _, required := range []string{
		"max-parallel: 2",
		"ghcr.io/tbphp/gpt-load",
		"tbphp/gpt-load",
		"RELEASE_SMOKE_SOURCE_IMAGE",
		".github/scripts/release-docker-smoke.sh",
	} {
		if !strings.Contains(smokeJob, required) {
			t.Fatalf("published image smoke job does not contain %q:\n%s", required, smokeJob)
		}
	}

	verifyJob := workflowJobBlock(t, content, "post-publish-verify")
	if strings.Contains(verifyJob, "post-publish-image-smoke") ||
		strings.Contains(verifyJob, "post-publish-native-smoke") {
		t.Fatalf("independent post-publication gates are still serialized:\n%s", verifyJob)
	}
	if strings.Contains(verifyJob, "for source_and_suffix in") {
		t.Fatalf("post-publication verification still runs image smokes serially:\n%s", verifyJob)
	}
	reconcileJob := workflowJobBlock(t, content, "reconcile-publication")
	for _, dependency := range []string{"post-publish-native-smoke", "post-publish-image-smoke", "post-publish-verify"} {
		if !strings.Contains(reconcileJob, dependency) {
			t.Fatalf("publication reconciliation does not need %s:\n%s", dependency, reconcileJob)
		}
	}
}
