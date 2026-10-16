package source

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestReadCSVURLColumnKeepsOrderAndDedupes(t *testing.T) {
	in := "\xef\xbb\xbfurl,views\nhttps://ex.com/b,1\nhttps://ex.com/a,9\nhttps://ex.com/b,1\n# comment\n\n"
	got, err := ReadCSV(strings.NewReader(in), "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://ex.com/b", "https://ex.com/a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestReadCSVPathColumn(t *testing.T) {
	got, err := ReadCSV(strings.NewReader("path\n/\n/blog/\n"), "https://ex.com")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://ex.com/", "https://ex.com/blog/"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if _, err := ReadCSV(strings.NewReader("path\n/\n"), ""); err == nil {
		t.Fatal("path column without --base-url: want error")
	}
}

func TestReadCSVErrors(t *testing.T) {
	if _, err := ReadCSV(strings.NewReader("name\n/x\n"), ""); !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("got %v, want ErrUnknownFormat", err)
	}
	if _, err := ReadCSV(strings.NewReader("url\n"), ""); !errors.Is(err, ErrEmpty) {
		t.Fatalf("got %v, want ErrEmpty", err)
	}
	if _, err := ReadCSV(strings.NewReader("url\n/relative\n"), ""); err == nil {
		t.Fatal("relative URL in url column: want error")
	}
}
