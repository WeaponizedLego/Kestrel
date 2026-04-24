package platform

import "testing"

func TestFileURI(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/plain/path.gif", "file:///plain/path.gif"},
		{"/with space/x.gif", "file:///with%20space/x.gif"},
		{"/tilde~weird/x.gif", "file:///tilde~weird/x.gif"},
		{"/percent%in/x.gif", "file:///percent%25in/x.gif"},
	}
	for _, tc := range cases {
		if got := fileURI(tc.path); got != tc.want {
			t.Errorf("fileURI(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestCopyImageToClipboardRejectsRelativePath(t *testing.T) {
	if err := CopyImageToClipboard("relative/path.gif"); err == nil {
		t.Fatal("expected error for relative path")
	}
}

func TestCopyImageToClipboardRejectsMissingFile(t *testing.T) {
	if err := CopyImageToClipboard("/nonexistent/definitely-not-here.gif"); err == nil {
		t.Fatal("expected error for missing file")
	}
}
