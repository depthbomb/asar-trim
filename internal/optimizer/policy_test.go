package optimizer

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestPolicyProfiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		profile Profile
		path    string
		want    Action
	}{
		// Safe contains files that cannot affect application execution.
		{name: "safe editor metadata", profile: ProfileSafe, path: "node_modules/example/.editorconfig", want: ActionRemove},
		{name: "safe dependency test", profile: ProfileSafe, path: "node_modules/example/test/widget.spec.js", want: ActionRemove},
		{name: "safe dependency documentation", profile: ProfileSafe, path: "node_modules/example/README.md", want: ActionRemove},
		{name: "safe dependency declarations", profile: ProfileSafe, path: "node_modules/example/index.d.ts", want: ActionRemove},
		{name: "safe source map is retained", profile: ProfileSafe, path: "dist/renderer.js.map", want: ActionKeep},
		{name: "safe typescript source is retained", profile: ProfileSafe, path: "src/renderer.ts", want: ActionKeep},

		// Balanced also removes common development-only output, while retaining
		// source files because Electron applications sometimes load them directly.
		{name: "balanced source map", profile: ProfileBalanced, path: "dist/renderer.js.map", want: ActionRemove},
		{name: "balanced test file", profile: ProfileBalanced, path: "node_modules/example/test/widget.spec.js", want: ActionRemove},
		{name: "balanced typescript source is retained", profile: ProfileBalanced, path: "src/renderer.ts", want: ActionKeep},

		// Aggressive broadly removes development source
		// and source-adjacent assets which are normally build inputs.
		{name: "aggressive typescript", profile: ProfileAggressive, path: "src/renderer.ts", want: ActionRemove},
		{name: "aggressive scss", profile: ProfileAggressive, path: "styles/theme.scss", want: ActionRemove},

		// Licenses and notices should never be built-in removal candidates.
		{name: "safe license", profile: ProfileSafe, path: "node_modules/example/LICENSE", want: ActionKeep},
		{name: "aggressive license", profile: ProfileAggressive, path: "node_modules/example/LICENSE.txt", want: ActionKeep},
		{name: "aggressive markdown license", profile: ProfileAggressive, path: "node_modules/example/LICENSE.md", want: ActionKeep},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p, err := newPolicy(Options{Profile: tt.profile})
			if err != nil {
				t.Fatalf("newPolicy() error = %v", err)
			}
			got := p.decide(tt.path)
			if got.Action != tt.want {
				t.Errorf("decide(%q).Action = %v, want %v (reason: %q)", tt.path, got.Action, tt.want, got.Reason)
			}
		})
	}
}

func TestPolicyOverridesAndPathNormalization(t *testing.T) {
	t.Parallel()

	p, err := newPolicy(Options{
		Profile:        ProfileAggressive,
		KeepPatterns:   []string{"vendor/**/runtime.ts", "**/keep.map"},
		RemovePatterns: []string{"vendor/**", "assets/**/*.bin"},
	})
	if err != nil {
		t.Fatalf("newPolicy() error = %v", err)
	}

	tests := []struct {
		name string
		path string
		want Action
	}{
		{name: "remove override beats profile keep", path: "assets/cache/index.bin", want: ActionRemove},
		{name: "doublestar matches zero directories", path: "assets/index.bin", want: ActionRemove},
		{name: "keep beats remove override", path: "vendor/pkg/runtime.ts", want: ActionKeep},
		{name: "keep beats aggressive profile", path: "maps/keep.map", want: ActionKeep},
		{name: "backslashes are normalized", path: `vendor\pkg\runtime.ts`, want: ActionKeep},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := p.decide(tt.path)
			if got.Action != tt.want {
				t.Errorf("decide(%q).Action = %v, want %v (reason: %q)", tt.path, got.Action, tt.want, got.Reason)
			}
			if got.Reason == "" {
				t.Errorf("decide(%q).Reason is empty", tt.path)
			}
		})
	}
}

func TestPolicyRejectsUnknownProfile(t *testing.T) {
	t.Parallel()

	if _, err := newPolicy(Options{Profile: Profile("reckless")}); err == nil {
		t.Fatal("newPolicy() error = nil, want an error for an unknown profile")
	}
}

func TestTransformMinifiesJSON(t *testing.T) {
	t.Parallel()

	p, err := newPolicy(Options{Profile: ProfileSafe, MinifyJSON: true})
	if err != nil {
		t.Fatalf("newPolicy() error = %v", err)
	}

	input := []byte("{\n  \"name\": \"demo\",\n  \"enabled\": true,\n  \"items\": [1, 2, 3]\n}\n")
	got, reason, err := p.transform("config/settings.json", input)
	if err != nil {
		t.Fatalf("transform() error = %v", err)
	}
	if string(got) != `{"name":"demo","enabled":true,"items":[1,2,3]}` {
		t.Errorf("transform() output = %q", got)
	}
	if len(got) >= len(input) {
		t.Errorf("transform() length = %d, want less than input length %d", len(got), len(input))
	}
	if reason == "" {
		t.Error("transform() reason is empty for changed JSON")
	}
}

func TestTransformPrunesPackageJSONConservatively(t *testing.T) {
	t.Parallel()

	p, err := newPolicy(Options{
		Profile:      ProfileBalanced,
		MinifyJSON:   true,
		PrunePackage: true,
	})
	if err != nil {
		t.Fatalf("newPolicy() error = %v", err)
	}

	input := []byte(`{
  "name": "demo",
  "version": "1.2.3",
  "main": "dist/main.js",
  "type": "module",
  "exports": {".": "./dist/main.js"},
  "dependencies": {"runtime-dep": "1.0.0"},
  "optionalDependencies": {"native-runtime": "1.0.0"},
  "devDependencies": {"typescript": "latest"},
  "scripts": {"test": "node test.js"},
  "description": "development metadata",
  "keywords": ["electron"],
  "repository": "example/demo"
}`)

	got, reason, err := p.transform("node_modules/demo/package.json", input)
	if err != nil {
		t.Fatalf("transform() error = %v", err)
	}
	if reason == "" {
		t.Error("transform() reason is empty for pruned package.json")
	}

	var pkg map[string]json.RawMessage
	if err := json.Unmarshal(got, &pkg); err != nil {
		t.Fatalf("transformed package.json is invalid: %v\n%s", err, got)
	}
	for _, key := range []string{"name", "version", "main", "type", "exports", "dependencies", "optionalDependencies"} {
		if _, ok := pkg[key]; !ok {
			t.Errorf("runtime-relevant property %q was pruned", key)
		}
	}
	for _, key := range []string{"devDependencies", "scripts", "description", "keywords", "repository"} {
		if _, ok := pkg[key]; ok {
			t.Errorf("development-only property %q was retained", key)
		}
	}
}

func TestTransformLeavesInvalidAndDisabledJSONUntouched(t *testing.T) {
	t.Parallel()

	t.Run("invalid JSON is a non-fatal no-op", func(t *testing.T) {
		p, err := newPolicy(Options{Profile: ProfileSafe, MinifyJSON: true})
		if err != nil {
			t.Fatalf("newPolicy() error = %v", err)
		}
		input := []byte(`{"unterminated":`)
		got, reason, err := p.transform("config.json", input)
		if err != nil {
			t.Fatalf("transform() error = %v, want non-fatal no-op", err)
		}
		if string(got) != string(input) {
			t.Errorf("transform() = %q, want original %q", got, input)
		}
		if reason != "" {
			t.Errorf("transform() reason = %q, want empty for no-op", reason)
		}
	})

	t.Run("minification disabled", func(t *testing.T) {
		p, err := newPolicy(Options{Profile: ProfileSafe})
		if err != nil {
			t.Fatalf("newPolicy() error = %v", err)
		}
		input := []byte("{\n  \"valid\": true\n}\n")
		got, reason, err := p.transform("config.json", input)
		if err != nil {
			t.Fatalf("transform() error = %v", err)
		}
		if string(got) != string(input) {
			t.Errorf("transform() = %q, want original %q", got, input)
		}
		if reason != "" {
			t.Errorf("transform() reason = %q, want empty for no-op", reason)
		}
	})
}

func TestTransformRecognizesPackageJSONWithPlatformSeparators(t *testing.T) {
	t.Parallel()

	p, err := newPolicy(Options{Profile: ProfileSafe, PrunePackage: true})
	if err != nil {
		t.Fatalf("newPolicy() error = %v", err)
	}
	input := []byte(`{"name":"demo","devDependencies":{"typescript":"latest"}}`)
	path := filepath.Join("node_modules", "demo", "package.json")
	got, _, err := p.transform(path, input)
	if err != nil {
		t.Fatalf("transform() error = %v", err)
	}
	if strings.Contains(string(got), "devDependencies") {
		t.Errorf("transform() did not recognize package.json path: %s", got)
	}
}
