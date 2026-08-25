package optimizer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func resourceLocalePlan(input string, opts Options) (*ResourceLocalePlan, error) {
	if !opts.TrimElectronLocales {
		return nil, nil
	}
	info, err := os.Stat(input)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("trim-electron-locales requires a resources directory input, not a direct ASAR path")
	}
	plan, err := PlanResourceLocalePruning(input, opts.Locales)
	if err != nil {
		return nil, err
	}
	return &plan, nil
}

func appendResourceLocaleRecommendation(report *Report, plan *ResourceLocalePlan) {
	if plan == nil || len(plan.Candidates) == 0 {
		return
	}
	paths := make([]string, 0, len(plan.Candidates))
	var bytes int64
	for _, candidate := range plan.Candidates {
		paths = append(paths, candidate.Path)
		bytes += candidate.Bytes
	}
	report.Recommendations = append(report.Recommendations, Recommendation{Kind: "chromium-locales", Message: fmt.Sprintf("remove %d Chromium locale packs outside the ASAR using the explicit locale allowlist", len(paths)), Paths: paths, EstimatedBytes: bytes})
}

type resourceLocaleStage struct {
	plan   *ResourceLocalePlan
	temp   string
	moved  map[string]string
	staged bool
}

func newResourceLocaleStage(plan *ResourceLocalePlan) *resourceLocaleStage {
	return &resourceLocaleStage{plan: plan, moved: make(map[string]string)}
}

func (s *resourceLocaleStage) stage() error {
	if s == nil || s.plan == nil || len(s.plan.Candidates) == 0 {
		return nil
	}
	var err error
	s.temp, err = os.MkdirTemp(s.plan.ResourcesDir, ".asar-trim-locales-*")
	if err != nil {
		return err
	}
	for _, candidate := range s.plan.Candidates {
		name := candidate.Path
		resolved, err := filepath.Abs(name)
		if err != nil || filepath.Dir(resolved) != filepath.Join(s.plan.ResourcesDir, "locales") {
			s.rollback()
			return fmt.Errorf("locale candidate escaped resources/locales: %q", name)
		}
		dest := filepath.Join(s.temp, filepath.Base(resolved))
		if err := os.Rename(resolved, dest); err != nil {
			s.rollback()
			return err
		}
		s.moved[resolved] = dest
	}
	s.staged = true
	return nil
}

func (s *resourceLocaleStage) rollback() {
	if s == nil {
		return
	}
	for original, staged := range s.moved {
		_ = os.Rename(staged, original)
	}
	if s.temp != "" {
		_ = os.RemoveAll(s.temp)
	}
	s.moved = make(map[string]string)
	s.staged = false
}

func (s *resourceLocaleStage) commit() error {
	if s == nil || !s.staged {
		return nil
	}
	if err := os.RemoveAll(s.temp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	s.moved = make(map[string]string)
	s.staged = false
	return nil
}
