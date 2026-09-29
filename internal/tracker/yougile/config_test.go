package yougile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validFile — tracker-yougile.yaml, который загрузчик обязан принять.
const validFile = `base_url: https://yougile.com
api_key_env: YOUGILE_API_KEY
also_agents: [bot@example.com]
projects:
  SHOP:
    project_id: proj-1
    columns:
      Ready: col-ready
      InProgress: col-work
    create_status: Ready
`

func writeFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), TrackerFile)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("%s не записан: %v", path, err)
	}
	return path
}

func TestLoadConfigReadsValidFile(t *testing.T) {
	fc, err := LoadConfig(writeFile(t, validFile))
	if err != nil {
		t.Fatalf("годный файл отвергнут: %v", err)
	}
	if fc.BaseURL != "https://yougile.com" || fc.APIKeyEnv != "YOUGILE_API_KEY" ||
		len(fc.AlsoAgents) != 1 || fc.AlsoAgents[0] != "bot@example.com" {
		t.Errorf("файл доехал не целиком: %+v", fc)
	}
	if fc.ProjectKey() != "SHOP" {
		t.Errorf("ProjectKey = %q, ожидался SHOP", fc.ProjectKey())
	}
	p := fc.Projects["SHOP"]
	if p.ProjectID != "proj-1" || p.Columns["InProgress"] != "col-work" || p.CreateStatus != "Ready" {
		t.Errorf("проект доехал не целиком: %+v", p)
	}
}

// Нет файла — отказ называет образец и `runner init`: сам файл не пишут,
// его копируют из образца.
func TestLoadConfigMissingFilePointsAtSample(t *testing.T) {
	_, err := LoadConfig(filepath.Join(t.TempDir(), TrackerFile))
	if err == nil {
		t.Fatal("отсутствующий файл принят")
	}
	for _, want := range []string{TrackerFile, ExampleFile, "runner init"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("отказ не назвал %q: %v", want, err)
		}
	}
}

func TestLoadConfigRejectsUnknownField(t *testing.T) {
	_, err := LoadConfig(writeFile(t, validFile+"sticker_id: s-1\n"))
	if err == nil || !strings.Contains(err.Error(), "sticker_id") {
		t.Errorf("неизвестное поле дало %v", err)
	}
}

// Пустой документ — не «не разобран: EOF», а перечень недостающего одним отказом.
func TestLoadConfigEmptyFileJoinsAllErrors(t *testing.T) {
	_, err := LoadConfig(writeFile(t, ""))
	if err == nil {
		t.Fatal("пустой файл принят")
	}
	for _, want := range []string{"нарушает контракт", "base_url", "api_key_env", "projects"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в отказе нет %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "EOF") {
		t.Errorf("пустой файл отвергнут как неразобранный: %v", err)
	}
}

func TestLoadConfigRejectsBrokenFile(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"нет base_url", strings.Replace(validFile, "base_url: https://yougile.com\n", "", 1), "base_url"},
		{"ru.", strings.Replace(validFile, "https://yougile.com", "https://ru.yougile.com", 1), "https://yougile.com"},
		{"ru. со слешем и в верхнем регистре", strings.Replace(validFile, "https://yougile.com", "https://RU.yougile.com/", 1), "вложения"},
		{"нет api_key_env", strings.Replace(validFile, "api_key_env: YOUGILE_API_KEY\n", "", 1), "api_key_env"},
		{"нет проектов", strings.Split(validFile, "projects:")[0], "projects"},
		{"два проекта", validFile + "  BLOG:\n    project_id: proj-2\n    columns: {Ready: c}\n", "один проект YouGile на раннер"},
		{"нет project_id", strings.Replace(validFile, "    project_id: proj-1\n", "", 1), "projects.SHOP.project_id"},
		{"пустые колонки", strings.Replace(validFile,
			"    columns:\n      Ready: col-ready\n      InProgress: col-work\n", "    columns: {}\n", 1), "projects.SHOP.columns"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadConfig(writeFile(t, tc.body))
			if err == nil {
				t.Fatal("битый файл принят")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("в отказе нет %q: %v", tc.want, err)
			}
		})
	}
}

// Отказ ru. говорит, почему: вложения не скачаются.
func TestLoadConfigRuHostExplainsAttachments(t *testing.T) {
	_, err := LoadConfig(writeFile(t, strings.Replace(validFile, "https://yougile.com", "https://ru.yougile.com", 1)))
	if err == nil || !strings.Contains(err.Error(), "вложения не скачаются") {
		t.Errorf("отказ ru. не объяснил причину: %v", err)
	}
}

func TestFileConfigTrackerBuildsAdapterConfig(t *testing.T) {
	fc, err := LoadConfig(writeFile(t, validFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOUGILE_API_KEY", "секрет")
	cfg, err := fc.Tracker("SHOP")
	if err != nil {
		t.Fatalf("Tracker: %v", err)
	}
	if cfg.Key != "SHOP" || cfg.ProjectID != "proj-1" || cfg.APIKey != "секрет" ||
		cfg.BaseURL != "https://yougile.com" || cfg.CreateStatus != "Ready" || cfg.ColumnIDs["Ready"] != "col-ready" {
		t.Errorf("Config собран не так: %+v", cfg)
	}
	// Карта колонок — копия: правка Config не должна тронуть FileConfig.
	cfg.ColumnIDs["Ready"] = "испорчено"
	if fc.Projects["SHOP"].Columns["Ready"] != "col-ready" {
		t.Error("ColumnIDs делит карту с FileConfig")
	}
}

// Пустая переменная — отказ с её именем, но без значения.
func TestFileConfigTrackerRejectsEmptyKey(t *testing.T) {
	fc, err := LoadConfig(writeFile(t, validFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOUGILE_API_KEY", "")
	if _, err := fc.Tracker("SHOP"); err == nil || !strings.Contains(err.Error(), "YOUGILE_API_KEY") {
		t.Errorf("пустой ключ дал %v", err)
	}
}

func TestFileConfigTrackerRejectsUnknownKey(t *testing.T) {
	fc, err := LoadConfig(writeFile(t, validFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOUGILE_API_KEY", "секрет")
	if _, err := fc.Tracker("BLOG"); err == nil || !strings.Contains(err.Error(), "BLOG") {
		t.Errorf("чужой ключ дал %v", err)
	}
}
