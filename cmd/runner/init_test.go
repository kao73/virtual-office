package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/runner"
	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/tracker/jira"
	payload "github.com/kao73/virtual-office/office"
)

// Свежая машина: каталога нет — появляется с двумя образцами конфигурации
// и каталогом scheduler/ на три задания планировщика, слово в слово из
// поставки, и с подсказкой, куда их копировать.
func TestInitLaysOutFreshHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "office-home") // ещё не существует
	t.Setenv(runner.HomeEnv, home)
	var out bytes.Buffer
	if err := initCommand(nil, &out); err != nil {
		t.Fatalf("init отказал: %v", err)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatalf("%s не прочитан: %v", home, err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	slices.Sort(names)
	// Образцы конфигурации — файлы, задания планировщика — каталог: «ровно»
	// осталось «ровно», просто список вырос на scheduler/. Убрать это
	// утверждение значило бы остаться без единственной проверки, что init
	// не кладёт в хозяйство ничего сверх обещанного.
	wantFiles := []string{tracker.ProjectsLocalExampleFile, jira.ExampleFile}
	wantTop := append(append([]string{}, wantFiles...), schedulerDir)
	slices.Sort(wantTop)
	if !slices.Equal(names, wantTop) {
		t.Errorf("в хозяйстве %v, ожидались ровно %v", names, wantTop)
	}
	for _, name := range wantFiles {
		got, err := os.ReadFile(filepath.Join(home, name))
		if err != nil {
			t.Fatalf("%s не прочитан: %v", name, err)
		}
		exp, err := fs.ReadFile(payload.Payload, name)
		if err != nil {
			t.Fatalf("%s не найден в поставке: %v", name, err)
		}
		if !bytes.Equal(got, exp) {
			t.Errorf("%s отличается от образца в поставке", name)
		}
	}
	// scheduler/ — ровно три задания, побайтно из поставки. Все три на любой
	// платформе: машина, на которой юнит правят, не всегда та, на которой он
	// работает, и выбирать по GOOS значило бы вешать теги сборки на данные.
	schedEntries, err := os.ReadDir(filepath.Join(home, schedulerDir))
	if err != nil {
		t.Fatalf("%s не прочитан: %v", schedulerDir, err)
	}
	var schedNames []string
	for _, e := range schedEntries {
		schedNames = append(schedNames, e.Name())
	}
	slices.Sort(schedNames)
	wantSched := append([]string{}, schedulerSamples...)
	slices.Sort(wantSched)
	if !slices.Equal(schedNames, wantSched) {
		t.Errorf("в %s %v, ожидались ровно %v", schedulerDir, schedNames, wantSched)
	}
	for _, name := range wantSched {
		got, err := os.ReadFile(filepath.Join(home, schedulerDir, name))
		if err != nil {
			t.Fatalf("%s не прочитан: %v", name, err)
		}
		exp, err := fs.ReadFile(payload.Payload, schedulerDir+"/"+name)
		if err != nil {
			t.Fatalf("%s не найден в поставке: %v", name, err)
		}
		if !bytes.Equal(got, exp) {
			t.Errorf("%s отличается от образца в поставке", name)
		}
	}
	printed := out.String()
	if got := strings.Count(printed, "создан"); got != 5 {
		t.Errorf("«создан» в выводе %d, ожидалось пять (два образца и три задания):\n%s", got, printed)
	}
	for _, want := range []string{tracker.ProjectsLocalFile, jira.TrackerFile, "дальше"} {
		if !strings.Contains(printed, want) {
			t.Errorf("вывод не называет %q:\n%s", want, printed)
		}
	}
}

// Настроенная машина: рабочие файлы и правленый образец не тронуты, код 0.
func TestInitLeavesConfiguredHomeAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv(runner.HomeEnv, home)
	files := map[string]string{
		tracker.ProjectsLocalFile:        "PROJ: {repo_url: x, default_branch: main, tracker: mock}\n",
		jira.TrackerFile:                 "base_url: http://jira\n",
		tracker.ProjectsLocalExampleFile: "# правленый образец\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(home, name), []byte(body), 0o644); err != nil {
			t.Fatalf("%s не записан: %v", name, err)
		}
	}
	var out bytes.Buffer
	if err := initCommand(nil, &out); err != nil { // nil error — код возврата 0
		t.Fatalf("init отказал на настроенном хозяйстве: %v", err)
	}
	for name, body := range files {
		got, err := os.ReadFile(filepath.Join(home, name))
		if err != nil {
			t.Fatalf("%s не прочитан: %v", name, err)
		}
		if string(got) != body {
			t.Errorf("%s изменён: было %q, стало %q", name, body, got)
		}
	}
	printed := out.String()
	if !strings.Contains(printed, "оставлен") || !strings.Contains(printed, tracker.ProjectsLocalExampleFile) {
		t.Errorf("вывод не сообщает об оставленном образце:\n%s", printed)
	}
	// jira.ExampleFile (единственный отсутствовавший образец) теперь существует
	if _, err := os.Stat(filepath.Join(home, jira.ExampleFile)); err != nil {
		t.Errorf("%s не создан: %v", jira.ExampleFile, err)
	}
}

// Правленое задание переживает второй init: в нём уже стоят настоящая учётка
// и путь, и переписать его значило бы снести настройку машины одной командой.
func TestInitLeavesEditedSchedulerSampleAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv(runner.HomeEnv, home)

	var first bytes.Buffer
	if err := initCommand(nil, &first); err != nil {
		t.Fatalf("первый init отказал: %v", err)
	}

	edited := filepath.Join(home, schedulerDir, "office-runner.service")
	body := "ExecStart=/home/owner/.office/bin/runner tick\n"
	if err := os.WriteFile(edited, []byte(body), 0o644); err != nil {
		t.Fatalf("%s не записан: %v", edited, err)
	}

	var second bytes.Buffer
	if err := initCommand(nil, &second); err != nil { // nil error — код возврата 0
		t.Fatalf("второй init отказал: %v", err)
	}
	got, err := os.ReadFile(edited)
	if err != nil {
		t.Fatalf("%s не прочитан: %v", edited, err)
	}
	if string(got) != body {
		t.Errorf("правленое задание изменено: было %q, стало %q", body, got)
	}
	printed := second.String()
	if !strings.Contains(printed, "оставлен") || !strings.Contains(printed, edited) {
		t.Errorf("вывод не сообщает об оставленном задании:\n%s", printed)
	}
	if strings.Contains(printed, "создан") {
		t.Errorf("второй init что-то создал:\n%s", printed)
	}
}

// Запись оборвалась (место кончилось, права отозвали) — огрызок убирается,
// а отказ называет исходную причину, а не молчит о ней.
func TestCleanupPartialWriteRemovesFile(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "office-runner.service")
	if err := os.WriteFile(dst, []byte("огрызок"), 0o644); err != nil {
		t.Fatalf("%s не записан: %v", dst, err)
	}
	writeErr := errors.New("нет места")

	err := cleanupPartialWrite(dst, writeErr)
	if err == nil || !errors.Is(err, writeErr) {
		t.Fatalf("отказ не называет исходную причину: %v", err)
	}
	if strings.Contains(err.Error(), "не убран") {
		t.Errorf("Remove сам не отказывал, а отказ говорит обратное: %v", err)
	}
	if _, statErr := os.Stat(dst); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("огрызок не убран: %v", statErr)
	}
}

// Вторая беда (Remove тоже не вышел) не должна прятаться за первой: иначе
// огрызок на следующем init попадёт в ветку fs.ErrExist и сойдёт за оставленный.
func TestCleanupPartialWriteReportsRemoveFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root игнорирует права каталога — убрать файл получится всё равно")
	}
	dir := t.TempDir()
	dst := filepath.Join(dir, "office-runner.service")
	if err := os.WriteFile(dst, []byte("огрызок"), 0o644); err != nil {
		t.Fatalf("%s не записан: %v", dst, err)
	}
	// Удаление файла требует права записи на каталог, а не на сам файл.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("права каталога не изменены: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) }) // иначе t.TempDir() не уберёт за собой

	writeErr := errors.New("нет места")
	err := cleanupPartialWrite(dst, writeErr)
	if err == nil {
		t.Fatalf("Remove должен был отказать под read-only каталогом")
	}
	if !errors.Is(err, writeErr) || !strings.Contains(err.Error(), "не убран") {
		t.Errorf("отказ не называет обе беды — запись и уборку: %v", err)
	}
	if _, statErr := os.Stat(dst); statErr != nil {
		t.Errorf("файл, который не убрался, должен остаться на месте: %v", statErr)
	}
}

// Каталог на месте образца — не наша правка и не чужой правленый юнит,
// а что-то третье. «Оставлен» сказал бы про него то же, что про честный файл.
func TestPlaceRejectsNonRegularExisting(t *testing.T) {
	home := t.TempDir()
	dst := filepath.Join(home, jira.ExampleFile)
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatalf("%s не создан: %v", dst, err)
	}

	var out bytes.Buffer
	err := place(&out, jira.ExampleFile, dst)
	if err == nil {
		t.Fatalf("place не отказал на каталоге вместо образца")
	}
	if strings.Contains(out.String(), "оставлен") {
		t.Errorf("каталог выдан за оставленный образец: %s", out.String())
	}
	info, statErr := os.Stat(dst)
	if statErr != nil || !info.IsDir() {
		t.Errorf("каталог на месте образца тронут: %v, %v", info, statErr)
	}
}
