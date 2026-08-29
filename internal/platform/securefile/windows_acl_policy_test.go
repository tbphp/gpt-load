package securefile

import "testing"

func TestWindowsServiceACLGrantsOnlyServiceAndAdministrators(t *testing.T) {
	const (
		serviceSID        = "S-1-5-80-111-222-333-444-555"
		administratorsSID = "S-1-5-32-544"
	)

	if got, want := windowsServiceFileSDDL(serviceSID, administratorsSID),
		"D:P(A;;FA;;;S-1-5-80-111-222-333-444-555)(A;;FA;;;S-1-5-32-544)"; got != want {
		t.Fatalf("file SDDL = %q, want %q", got, want)
	}
	if got, want := windowsServiceDirectorySDDL(serviceSID, administratorsSID),
		"D:P(A;OICI;FA;;;S-1-5-80-111-222-333-444-555)(A;OICI;FA;;;S-1-5-32-544)"; got != want {
		t.Fatalf("directory SDDL = %q, want %q", got, want)
	}
}
