package meads

import "testing"

func TestDetectFormat_CSV(t *testing.T) {
	f := detectFormat("TASKS.csv")
	if _, ok := f.(csvFormat); !ok {
		t.Errorf("detectFormat(TASKS.csv) returned %T, want csvFormat", f)
	}
}

func TestDetectFormat_MD(t *testing.T) {
	f := detectFormat("TASKS.md")
	if _, ok := f.(markdownFormat); !ok {
		t.Errorf("detectFormat(TASKS.md) returned %T, want markdownFormat", f)
	}
}

func TestDetectFormat_Default(t *testing.T) {
	f := detectFormat("TASKS.txt")
	if _, ok := f.(markdownFormat); !ok {
		t.Errorf("detectFormat(TASKS.txt) returned %T, want markdownFormat", f)
	}
}
