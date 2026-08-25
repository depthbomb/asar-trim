package optimizer

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripSourceMapSourcesContent(t *testing.T) {
	in := []byte(`{"version":3,"sources":["src/a.ts"],"sourcesContent":["const a = 1"],"mappings":"AAAA"}`)
	out, changed, err := StripSourceMapSourcesContent(in)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected a rewrite")
	}
	if bytes.Contains(out, []byte("sourcesContent")) {
		t.Fatalf("embedded source survived: %s", out)
	}
	if !bytes.Contains(out, []byte(`"mappings":"AAAA"`)) {
		t.Fatalf("mapping was lost: %s", out)
	}

	if _, _, err := StripSourceMapSourcesContent([]byte(`{`)); err == nil {
		t.Fatal("invalid source map should fail")
	}
}

func TestRemoveSourceMappingURL(t *testing.T) {
	for _, in := range []string{
		"const x = 1;\n//# sourceMappingURL=x.js.map\n",
		"body{}\n/*# sourceMappingURL=x.css.map */",
		"//# sourceMappingURL=data:application/json;base64,e30=\n",
	} {
		out, changed := RemoveSourceMappingURL([]byte(in))
		if !changed || bytes.Contains(out, []byte("sourceMappingURL")) {
			t.Fatalf("directive survived: %q", out)
		}
	}
}

func TestPlanSourceMapExternalization(t *testing.T) {
	plan, err := PlanSourceMapExternalization(
		"dist/app.js", "dist/app.js.map",
		[]byte("x();\n//# sourceMappingURL=app.js.map\n"),
		[]byte(`{"version":3,"sourcesContent":["x()"]}`),
		t.TempDir(), true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plan.AssetData), "sourceMappingURL") {
		t.Fatal("asset directive survived")
	}
	if !plan.StrippedSourcesContent || strings.Contains(string(plan.MapData), "sourcesContent") {
		t.Fatal("sourcesContent survived")
	}
	if filepath.Base(plan.DestinationPath) != "app.js.map" {
		t.Fatalf("bad destination: %q", plan.DestinationPath)
	}
	if _, err := PlanSourceMapExternalization("app.js", "../secret.map", nil, []byte(`{}`), t.TempDir(), false); err == nil {
		t.Fatal("archive traversal should fail")
	}
}

func TestMinifyAssetConservatively(t *testing.T) {
	js := []byte("#!/usr/bin/env node\nconst  x = 'a  b'; // retain me\nreturn\n  x;\n")
	out, changed, err := MinifyAsset("tool.js", js)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected horizontal whitespace reduction")
	}
	if !bytes.Contains(out, []byte("'a  b'")) || !bytes.Contains(out, []byte("// retain me")) || !bytes.Contains(out, []byte("return\n")) {
		t.Fatalf("semantic boundary changed: %q", out)
	}

	html := []byte("  <main>\n  <span>x</span>\n</main><!-- build note -->  ")
	out, changed, err = MinifyAsset("index.html", html)
	if err != nil || !changed {
		t.Fatalf("html minification: changed=%v err=%v", changed, err)
	}
	if string(out) != "<main><span>x</span></main>" {
		t.Fatalf("unexpected html: %q", out)
	}

	legal := []byte("<svg><!-- Copyright 2026 Acme --><path /></svg>")
	out, _, err = MinifyAsset("icon.svg", legal)
	if err != nil || !bytes.Contains(out, []byte("Copyright")) {
		t.Fatalf("legal comment removed: %q (%v)", out, err)
	}
	if _, _, err := MinifyAsset("file.txt", []byte("x")); err == nil {
		t.Fatal("unsupported extension should fail")
	}
}

func TestIsLicenseFile(t *testing.T) {
	for _, path := range []string{"LICENSE", "node_modules/a/LICENSE.md", "NOTICE.txt", "LICENCE-MIT", "COPYING"} {
		if !IsLicenseFile(path) {
			t.Errorf("did not recognize %q", path)
		}
	}
	for _, path := range []string{"licensed.js", "license-checker.js", "docs/copyright-guide.md"} {
		if IsLicenseFile(path) {
			t.Errorf("false positive %q", path)
		}
	}
}

func TestStripLicenseHeaderPreservesShebangAndCode(t *testing.T) {
	in := []byte("#!/usr/bin/env node\n/*! Copyright 2026 Acme. Licensed under MIT. */\n\nconst x = 1;\n")
	out, changed := StripLicenseHeader("cli.js", in)
	if !changed {
		t.Fatal("expected header removal")
	}
	if !bytes.HasPrefix(out, []byte("#!/usr/bin/env node\n")) {
		t.Fatalf("shebang lost: %q", out)
	}
	if bytes.Contains(out, []byte("Copyright")) || !bytes.Contains(out, []byte("const x = 1")) {
		t.Fatalf("wrong output: %q", out)
	}
}

func TestStripLicenseHeaderRequiresMarkerAndLeadingPosition(t *testing.T) {
	for _, in := range []string{
		"/* ordinary implementation note */\nconst x = 1;",
		"const x = 1;\n/* Copyright 2026 Acme */",
	} {
		out, changed := StripLicenseHeader("app.js", []byte(in))
		if changed || string(out) != in {
			t.Fatalf("unexpected removal from %q", in)
		}
	}
	if out, changed := StripLicenseHeader("README.md", []byte("<!-- Copyright Acme -->")); changed || string(out) == "" {
		t.Fatal("unsupported content type should not be rewritten")
	}
}

func TestStripConsecutiveLineLicenseHeader(t *testing.T) {
	in := []byte("// Copyright 2026 Acme\n// SPDX-License-Identifier: MIT\n\nfunction main() {}\n")
	out, changed := StripLicenseHeader("app.js", in)
	if !changed || string(out) != "function main() {}\n" {
		t.Fatalf("unexpected result: %q", out)
	}
}

func TestStripHashLicenseHeader(t *testing.T) {
	in := []byte("#!/usr/bin/env python3\n# Copyright 2026 Acme\n# SPDX-License-Identifier: MIT\n\nprint('ok')\n")
	out, changed := StripLicenseHeader("tool.py", in)
	if !changed || string(out) != "#!/usr/bin/env python3\nprint('ok')\n" {
		t.Fatalf("unexpected result: %q", out)
	}
}

func TestStripLicenseHeaderPreservesUTF8BOM(t *testing.T) {
	in := append([]byte{0xef, 0xbb, 0xbf}, []byte("/* Copyright 2026 Acme */\nconst x = 1;\n")...)
	out, changed := StripLicenseHeader("app.js", in)
	if !changed || !bytes.HasPrefix(out, []byte{0xef, 0xbb, 0xbf}) || bytes.Contains(out, []byte("Copyright")) {
		t.Fatalf("unexpected result: %q", out)
	}
}
