package graph

import (
	"errors"
	"strings"
	"testing"
)

// A path with backslashes addresses the same file as one with forward
// slashes. Left as one segment it was escaped to %5C and could never resolve.
func TestPathURL_BackslashesAreSeparators(t *testing.T) {
	want := "https://graph.microsoft.com/v1.0/drives/D/root:/POA/noreply@supplier.co.uk/Scanned%20File/Sales%20Order%20SO1.pdf:/content"
	for _, path := range []string{
		"POA/noreply@supplier.co.uk/Scanned File/Sales Order SO1.pdf",
		`POA\noreply@supplier.co.uk\Scanned File\Sales Order SO1.pdf`,
		`\POA\noreply@supplier.co.uk/Scanned File\Sales Order SO1.pdf`,
		"/POA/noreply@supplier.co.uk/Scanned File/Sales Order SO1.pdf",
	} {
		if got := pathURL("D", path, "/content"); got != want {
			t.Errorf("pathURL(%q)\n got %s\nwant %s", path, got, want)
		}
	}
}

// The `\n` in `POA\noreply@...` is a backslash and an n, and must stay an n:
// nothing between the request and Graph may read it as a newline.
func TestPathURL_BackslashNIsNotANewline(t *testing.T) {
	got := pathURL("D", `POA\noreply@supplier.co.uk\x.pdf`, "")
	if strings.ContainsAny(got, "\n\r") || strings.Contains(got, "%0A") {
		t.Errorf("path was unescaped into a newline: %q", got)
	}
	if !strings.Contains(got, "/POA/noreply@supplier.co.uk/x.pdf:") {
		t.Errorf("got %q", got)
	}
}

func TestPathErr_NamesThePathAskedFor(t *testing.T) {
	cause := errors.New("boom")
	err := pathErr(`POA\a\x.pdf`, cause)
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), `"POA/a/x.pdf"`) {
		t.Errorf("got %v", err)
	}
}
