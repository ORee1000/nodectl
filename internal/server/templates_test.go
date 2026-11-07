package server

import (
	"bytes"
	"html/template"
	"testing"
)

func TestSubscriptionTemplateRenders(t *testing.T) {
	tmpl, err := template.ParseGlob("../../templates/components/*.html")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := tmpl.ExecuteTemplate(&out, "sub_links_modal.html", nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("subProfileEditor")) {
		t.Fatal("subscription editor missing")
	}
}
