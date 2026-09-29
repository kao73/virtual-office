package yougile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/tracker"
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

// also_agents сравнивают с автором строкой, а тот приходит в нижнем регистре.
// Слеши на конце — всё ещё корень хоста: Open их срезает.
func TestLoadConfigAcceptsTrailingSlashes(t *testing.T) {
	for _, u := range []string{"https://yougile.com/", "https://yougile.com//"} {
		if _, err := LoadConfig(writeFile(t, strings.Replace(validFile, "https://yougile.com", u, 1))); err != nil {
			t.Errorf("%s отвергнут: %v", u, err)
		}
	}
}

func TestLoadConfigNormalizesAlsoAgents(t *testing.T) {
	fc, err := LoadConfig(writeFile(t, strings.Replace(validFile, "[bot@example.com]", `[" Bot@Example.COM "]`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	if len(fc.AlsoAgents) != 1 || fc.AlsoAgents[0] != "bot@example.com" {
		t.Errorf("also_agents = %q", fc.AlsoAgents)
	}
}

func TestCheckGraph(t *testing.T) {
	fc, err := LoadConfig(writeFile(t, validFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := fc.CheckGraph("SHOP", []string{"InProgress", "Ready"}); err != nil {
		t.Errorf("совпадающий граф отвергнут: %v", err)
	}
	if err := fc.CheckGraph("BLOG", []string{"Ready"}); err == nil || !strings.Contains(err.Error(), `не описывает проект "BLOG"`) {
		t.Errorf("чужой ключ дал %v", err)
	}
	cases := []struct {
		name     string
		statuses []string
		want     []string
	}{
		{"статусу нет колонки", []string{"Ready", "InProgress", "Done"}, []string{"у статусов графа Done нет колонки"}},
		{"колонка не из графа", []string{"Ready"}, []string{"называет InProgress", "таких статусов в графе нет"}},
		{"обе стороны", []string{"Ready", "Done"}, []string{"Done нет колонки", "называет InProgress"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := fc.CheckGraph("SHOP", tc.statuses)
			if err == nil {
				t.Fatal("расхождение с графом принято")
			}
			for _, want := range append(tc.want, TrackerFile) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("в отказе нет %q: %v", want, err)
				}
			}
		})
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
		{"ru.", strings.Replace(validFile, "https://yougile.com", "https://ru.yougile.com", 1), "вложения не скачаются"},
		{"ru. со слешем и в верхнем регистре", strings.Replace(validFile, "https://yougile.com", "https://RU.yougile.com/", 1), "вложения"},
		{"нет api_key_env", strings.Replace(validFile, "api_key_env: YOUGILE_API_KEY\n", "", 1), "api_key_env"},
		{"нет проектов", strings.Split(validFile, "projects:")[0], "projects"},
		{"два проекта", validFile + "  BLOG:\n    project_id: proj-2\n    columns: {Ready: c}\n", "один проект YouGile на раннер"},
		{"нет project_id", strings.Replace(validFile, "    project_id: proj-1\n", "", 1), "projects.SHOP.project_id"},
		{"пустые колонки", strings.Replace(validFile,
			"    columns:\n      Ready: col-ready\n      InProgress: col-work\n", "    columns: {}\n", 1), "projects.SHOP.columns"},
		{"base_url без схемы", strings.Replace(validFile, "https://yougile.com", "yougile.com", 1), "нет схемы или хоста"},
		{"base_url с /api-v2", strings.Replace(validFile, "https://yougile.com", "https://yougile.com/api-v2/", 1), "/api-v2"},
		{"base_url с /API-V2", strings.Replace(validFile, "https://yougile.com", "https://yougile.com/API-V2", 1), "/api-v2"},
		{"base_url с путём", strings.Replace(validFile, "https://yougile.com", "https://yougile.com/foo", 1), "без пути"},
		{"base_url с query", strings.Replace(validFile, "https://yougile.com", "https://yougile.com?x=1", 1), "без пути"},
		{"base_url с fragment", strings.Replace(validFile, "https://yougile.com", "https://yougile.com/#x", 1), "без пути"},
		{"пустой ключ проекта", strings.Replace(validFile, "  SHOP:", `  "":`, 1), "ключ проекта пуст"},
		{"ru. с точкой на конце", strings.Replace(validFile, "https://yougile.com", "https://ru.yougile.com.", 1), "вложения"},
		{"пустой also_agents", strings.Replace(validFile, "[bot@example.com]", "[bot@example.com, \" \"]", 1), "also_agents[1]"},
		{"нет create_status", strings.Replace(validFile, "    create_status: Ready\n", "", 1), "projects.SHOP.create_status не задан"},
		{"create_status вне columns", strings.Replace(validFile, "create_status: Ready", "create_status: Backlog", 1), `create_status="Backlog"`},
		{"пустой id колонки", strings.Replace(validFile, "InProgress: col-work", `InProgress: ""`, 1), "projects.SHOP.columns.InProgress"},
		{"одна колонка на два статуса", strings.Replace(validFile, "InProgress: col-work", "InProgress: col-ready", 1), "и с InProgress, и с Ready"},
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

// Поставляемый образец обязан загружаться как есть и сходиться с
// поставляемым графом: иначе первый же человек, скопировавший его, получит
// отказ не про свои значения, а про опечатку в образце или про статус,
// добавленный в граф без колонки в образце.
func TestShippedSampleLoads(t *testing.T) {
	office := filepath.Join("..", "..", "..", "office")
	fc, err := LoadConfig(filepath.Join(office, ExampleFile))
	if err != nil {
		t.Fatalf("образец %s не загружается: %v", ExampleFile, err)
	}
	workflow, err := tracker.LoadWorkflow(filepath.Join(office, tracker.WorkflowFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := fc.CheckGraph(fc.ProjectKey(), workflow.Statuses); err != nil {
		t.Errorf("образец расходится с графом: %v", err)
	}
	if fc.BaseURL != "https://yougile.com" {
		t.Errorf("образец советует base_url %q, а годится только https://yougile.com", fc.BaseURL)
	}
}
