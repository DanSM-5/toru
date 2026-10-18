package main

// Tests of the fzf pickers. fzf needs a terminal, so each scenario re-executes
// the test binary (TestPickerHelper) inside a private tmux server, drives it
// with keys, and reads back the result it writes.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DanSM-5/toru/pkg/nyaa"
)

const (
	pickerHelperEnv = "TORU_PICKER_HELPER"
	pickerResultEnv = "TORU_PICKER_RESULT"
)

// mockTitle is wrapped in the preview window of a 120 columns terminal, where
// wrapping by characters would split "featuring"
const mockTitle = "[Mock Group] Example Show (01-12) (1080p) [Batch] featuring a title long enough to wrap"

var mockEpisodes = []string{
	"Example Show - 03.mkv",
	"Example Show - 01.mkv",
	"Example Show - 02.mkv",
	"Extras/Example Show - NCOP.mkv",
}

var mockMedia = []nyaa.Media{
	{MediaPartial: nyaa.MediaPartial{
		Name:      mockTitle,
		Date:      time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Downloads: 42,
		Seeders:   7,
		Leechers:  3,
		Size:      1 << 30,
	}},
	{MediaPartial: nyaa.MediaPartial{Name: "[Mock Group] Another Show - 01 (720p)"}},
}

type pickerResult struct {
	Value []string
	Err   string
}

// TestPickerHelper is not a test by itself; it runs a scenario when executed
// by runPicker
func TestPickerHelper(t *testing.T) {
	scenario := os.Getenv(pickerHelperEnv)
	if scenario == "" {
		t.Skip("helper process for the picker tests")
	}

	var result pickerResult
	var err error
	switch scenario {
	case "episodes":
		result.Value, err = pickEpisodes(mockEpisodes)
	case "main":
		var action string
		action, err = fzfMain()
		result.Value = []string{action}
	case "menu":
		var media nyaa.Media
		media, err = fzfMenu(mockMedia)
		result.Value = []string{media.Name}
	case "abort":
		_, err = pick([]string{"one", "two"}, func(i int) string { return "item" })
	}
	if err != nil {
		result.Err = err.Error()
	}

	bytes, _ := json.Marshal(result)
	// Rename, so that the test never reads a partially written file
	path := os.Getenv(pickerResultEnv)
	if err := os.WriteFile(path+".tmp", bytes, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		t.Fatal(err)
	}
}

type pickerSession struct {
	t      *testing.T
	socket string
	result string
}

func (s *pickerSession) tmux(args ...string) string {
	cmd := exec.Command("tmux", append([]string{"-L", s.socket, "-f", "/dev/null"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.t.Fatalf("tmux %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (s *pickerSession) screen() string {
	return s.tmux("capture-pane", "-p")
}

func (s *pickerSession) waitFor(what string, cond func() bool) {
	s.t.Helper()
	for start := time.Now(); !cond(); time.Sleep(20 * time.Millisecond) {
		if time.Since(start) > 10*time.Second {
			s.t.Fatalf("timed out waiting for %s; screen:\n%s", what, s.screen())
		}
	}
}

func (s *pickerSession) waitForScreen(text string) {
	s.t.Helper()
	s.waitFor(fmt.Sprintf("%q on screen", text), func() bool {
		return strings.Contains(s.screen(), text)
	})
}

func (s *pickerSession) keys(keys ...string) {
	s.tmux(append([]string{"send-keys"}, keys...)...)
}

func (s *pickerSession) wait() pickerResult {
	s.t.Helper()
	s.waitFor("the result", func() bool {
		_, err := os.Stat(s.result)
		return err == nil
	})
	bytes, err := os.ReadFile(s.result)
	if err != nil {
		s.t.Fatal(err)
	}
	var result pickerResult
	if err := json.Unmarshal(bytes, &result); err != nil {
		s.t.Fatalf("%v: %s", err, bytes)
	}
	return result
}

func runPicker(t *testing.T, scenario string) *pickerSession {
	t.Helper()
	if testing.Short() {
		t.Skip("runs fzf in tmux")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is required")
	}
	s := &pickerSession{
		t:      t,
		socket: fmt.Sprintf("toru-test-%d-%s", os.Getpid(), scenario),
		result: filepath.Join(t.TempDir(), "result.json"),
	}
	command := fmt.Sprintf("env -u TMUX %s=%s %s=%s %s -test.run='^TestPickerHelper$'; sleep 60",
		pickerHelperEnv, scenario, pickerResultEnv, s.result, os.Args[0])
	s.tmux("new-session", "-d", "-x", "120", "-y", "24", command)
	t.Cleanup(func() {
		exec.Command("tmux", "-L", s.socket, "kill-server").Run()
	})
	return s
}

func TestPickEpisodesMulti(t *testing.T) {
	s := runPicker(t, "episodes")
	s.waitForScreen("4/4")
	s.waitForScreen("Tab to select multiple episodes")
	s.keys("Tab")
	s.keys("Tab")
	s.waitForScreen("(2)")
	s.keys("Enter")

	// The marked episodes, sorted to play in order
	want := []string{"Example Show - 01.mkv", "Example Show - 03.mkv"}
	if result := s.wait(); result.Err != "" || !reflect.DeepEqual(result.Value, want) {
		t.Errorf("result: %+v, want %v", result, want)
	}
}

func TestPickMainMenu(t *testing.T) {
	s := runPicker(t, "main")
	s.waitForScreen("replay")
	s.keys("Down")
	s.keys("Enter")

	if result := s.wait(); result.Err != "" || !reflect.DeepEqual(result.Value, []string{"select"}) {
		t.Errorf("result: %+v", result)
	}
}

func TestPickMenuPreview(t *testing.T) {
	s := runPicker(t, "menu")
	s.waitForScreen("Downloads: 42")

	// The title is wrapped by words in the preview, so every word is intact
	screen := s.screen()
	if !strings.Contains(screen, "↳") {
		t.Errorf("the title should be wrapped; screen:\n%s", screen)
	}
	preview := previewText(screen)
	for _, word := range strings.Fields(mockTitle) {
		if !strings.Contains(preview, word) {
			t.Errorf("word %q is not intact in the preview:\n%s", word, preview)
		}
	}

	s.keys("Enter")
	if result := s.wait(); result.Err != "" || !reflect.DeepEqual(result.Value, []string{mockTitle}) {
		t.Errorf("result: %+v", result)
	}
}

func TestPickNoMatches(t *testing.T) {
	s := runPicker(t, "abort")
	s.waitForScreen("2/2")
	s.keys("zzz")
	s.waitForScreen("No matches")
	s.keys("Escape")

	if result := s.wait(); result.Err != errAbort.Error() {
		t.Errorf("result: %+v", result)
	}
}

// previewText returns the text inside the preview window borders
func previewText(screen string) string {
	var lines []string
	for _, line := range strings.Split(screen, "\n") {
		// each line of the window is drawn as "│ text │" at the end of the screen line
		line = strings.TrimSuffix(strings.TrimRight(line, " "), "│")
		if i := strings.LastIndex(line, "│"); i >= 0 {
			lines = append(lines, strings.TrimSpace(line[i+len("│"):]))
		}
	}
	return strings.Join(lines, "\n")
}
