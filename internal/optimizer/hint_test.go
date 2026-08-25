package optimizer

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	asar "github.com/depthbomb/go-asar"
)

func TestGenerateHintFollowsStaticDependencies(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	mustWrite(t, filepath.Join(source, "package.json"), []byte(`{"main":"src/main.js"}`))
	mustWrite(t, filepath.Join(source, "src", "main.js"), []byte(`const local = require('./local'); import dep from 'dep'`))
	mustWrite(t, filepath.Join(source, "src", "local.js"), []byte(`module.exports = 1`))
	mustWrite(t, filepath.Join(source, "node_modules", "dep", "package.json"), []byte(`{"main":"lib/start.cjs"}`))
	mustWrite(t, filepath.Join(source, "node_modules", "dep", "lib", "start.cjs"), []byte(`module.exports = 2`))
	mustWrite(t, filepath.Join(source, "unused.js"), []byte(`unused`))
	archive := filepath.Join(root, "app.asar")
	if _, err := asar.Create(source, archive, asar.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "startup.hint")
	report, err := GenerateHint(archive, HintOptions{Output: output})
	if err != nil {
		t.Fatal(err)
	}
	hint, err := asar.ReadHint(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"package.json", "src/main.js", "src/local.js", "node_modules/dep/lib/start.cjs"} {
		if !slices.Contains(hint.Paths, want) {
			t.Errorf("hint paths %v do not contain %q", hint.Paths, want)
		}
	}
	if slices.Contains(hint.Paths, "unused.js") {
		t.Errorf("unreachable file included: %v", hint.Paths)
	}
	if report.Paths != len(hint.Paths) || report.Coverage <= 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if _, err := GenerateHint(archive, HintOptions{Output: output}); err == nil {
		t.Fatal("existing hint was overwritten without force")
	}
	if _, err := GenerateHint(archive, HintOptions{Output: output, Force: true}); err != nil {
		t.Fatalf("forced generation failed: %v", err)
	}
}

func TestOptimizeCanGenerateAndApplyHint(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	mustWrite(t, filepath.Join(source, "package.json"), []byte(`{"main":"index.js"}`))
	mustWrite(t, filepath.Join(source, "index.js"), []byte(`console.log('ok')`))
	archive := filepath.Join(root, "app.asar")
	if _, err := asar.Create(source, archive, asar.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	hint := filepath.Join(root, "startup.hint")
	output := filepath.Join(root, "optimized.asar")
	report, err := Optimize(archive, Options{Profile: ProfileSafe, Verify: true, Output: output, GenerateHint: hint})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Optimized || report.HintCoverage <= 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if _, err := os.Stat(hint); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateHintDiscoversModernAndElectronReferences(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	mustWrite(t, filepath.Join(source, "package.json"), []byte(`{"main":"main.js","imports":{"#config":"./config.js"}}`))
	mustWrite(t, filepath.Join(source, "main.js"), []byte(`
		const config = require('#config')
		import feature from 'dep/feature'
		new Worker('./worker.js')
		utilityProcess.fork('./utility.js')
		const windowOptions = { webPreferences: { preload: path.join(__dirname, 'preload.js') } }
	`))
	mustWrite(t, filepath.Join(source, "config.js"), []byte(`module.exports = 1`))
	mustWrite(t, filepath.Join(source, "worker.js"), []byte(`self.postMessage('ok')`))
	mustWrite(t, filepath.Join(source, "utility.js"), []byte(`process.send('ok')`))
	mustWrite(t, filepath.Join(source, "preload.js"), []byte(`console.log('preload')`))
	mustWrite(t, filepath.Join(source, "window.html"), []byte(`<script src="./renderer.js"></script>`))
	mustWrite(t, filepath.Join(source, "renderer.js"), []byte(`console.log('renderer')`))
	mustWrite(t, filepath.Join(source, "node_modules", "dep", "package.json"), []byte(`{"exports":{"./feature":{"electron":"./electron.js","default":"./default.js"}}}`))
	mustWrite(t, filepath.Join(source, "node_modules", "dep", "electron.js"), []byte(`export default 1`))
	mustWrite(t, filepath.Join(source, "node_modules", "dep", "default.js"), []byte(`export default 2`))
	archive := filepath.Join(root, "app.asar")
	if _, err := asar.Create(source, archive, asar.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "startup.hint")
	if _, err := GenerateHint(archive, HintOptions{Output: output, EntryPoints: []string{"window.html"}}); err != nil {
		t.Fatal(err)
	}
	hint, err := asar.ReadHint(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"config.js", "worker.js", "utility.js", "preload.js", "node_modules/dep/electron.js", "window.html", "renderer.js"} {
		if !slices.Contains(hint.Paths, want) {
			t.Errorf("hint paths %v do not contain %q", hint.Paths, want)
		}
	}
	if slices.Contains(hint.Paths, "node_modules/dep/default.js") {
		t.Errorf("lower-priority conditional export included: %v", hint.Paths)
	}
}
