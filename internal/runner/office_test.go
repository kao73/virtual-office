package runner

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	payload "github.com/kao73/virtual-office"
)

const rev = "9f2e1c4b7a3d5e6f8091a2b3c4d5e6f708192a3b"

// fakePayload подменяет поставку маленьким деревом: настоящие 13 МБ
// распаковывать в каждом тесте незачем, а форма та же.
func fakePayload(t *testing.T) fstest.MapFS {
	t.Helper()
	m := fstest.MapFS{
		"roles/_base/base.yaml":                  {Data: []byte("network: {}\n")},
		"hooks/require-result.sh":                {Data: []byte("#!/bin/sh\nexit 0\n")},
		"bootstrap/sbx-kits/comet-cli/spec.yaml": {Data: []byte("name: comet\n")},
		"workflow.yaml":                          {Data: []byte("statuses: []\n")},
	}
	prev := payloadFS
	payloadFS = m
	t.Cleanup(func() { payloadFS = prev })
	return m
}

// buildInfo подменяет build info бинарника; пустой rev — «build info нет».
func buildInfo(t *testing.T, rev string, modified bool) {
	t.Helper()
	prev := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		if rev == "" {
			return nil, false
		}
		return &debug.BuildInfo{Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: rev},
			{Key: "vcs.modified", Value: strconv.FormatBool(modified)},
		}}, true
	}
	t.Cleanup(func() { readBuildInfo = prev })
}

// releaseVersion подменяет версию, вшитую ldflags.
func releaseVersion(t *testing.T, v string) {
	t.Helper()
	prev := payload.Version
	payload.Version = v
	t.Cleanup(func() { payload.Version = prev })
}

// payloadHome — хозяйство без OFFICE_CONFIG_ROOT: режим поставки.
func payloadHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv(HomeEnv, home)
	t.Setenv(ConfigRootEnv, "")
	return home
}

// absent проверяет, что пути нет: ни каталога, ни файла.
func absent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s не должен существовать, а os.Stat дал: %v", path, err)
	}
}

// Переменная выигрывает у всего: офис из клона, личность — commit, поставка
// не трогается даже при вшитой версии.
func TestResolveOfficeCloneFollowsConfigRoot(t *testing.T) {
	home := payloadHome(t)
	fakePayload(t)
	releaseVersion(t, "v0.7.0")
	repo := gitRepo(t)
	t.Setenv(ConfigRootEnv, repo)

	o, err := ResolveOffice(Resolve{Unpack: true})
	if err != nil {
		t.Fatalf("ResolveOffice: %v", err)
	}
	if o.Source != SourceClone {
		t.Errorf("Source = %q, ждали %q", o.Source, SourceClone)
	}
	if o.Root != repo {
		t.Errorf("Root = %q, ждали %q", o.Root, repo)
	}
	head := strings.TrimSpace(git(t, repo, "rev-parse", "HEAD"))
	if o.Identity != head {
		t.Errorf("Identity = %q, ждали HEAD %q", o.Identity, head)
	}
	absent(t, filepath.Join(home, OfficeDir))

	if err := os.WriteFile(filepath.Join(repo, "x"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o2, err := ResolveOffice(Resolve{Unpack: true})
	if err != nil {
		t.Fatalf("ResolveOffice после правки: %v", err)
	}
	if o2.Identity != head+"-dirty" {
		t.Errorf("Identity грязного клона = %q, ждали %q", o2.Identity, head+"-dirty")
	}
	if d := o.Describe(); !strings.Contains(d, "клон "+repo) {
		t.Errorf("Describe() = %q, нет %q", d, "клон "+repo)
	}
}

func TestResolveOfficeReleaseVersionUnpacksOnce(t *testing.T) {
	home := payloadHome(t)
	fakePayload(t)
	releaseVersion(t, "v0.7.0")
	o, err := ResolveOffice(Resolve{Unpack: true})
	if err != nil {
		t.Fatalf("ResolveOffice: %v", err)
	}
	want := filepath.Join(home, OfficeDir, "v0.7.0")
	if wantOffice := (Office{Root: want, Identity: "v0.7.0", Source: SourcePayload}); o != wantOffice {
		t.Errorf("Office = %+v, ждали %+v", o, wantOffice)
	}
	for _, rel := range []string{"roles/_base/base.yaml", "sbx-kits/comet-cli/spec.yaml"} {
		if _, err := os.Stat(filepath.Join(want, rel)); err != nil {
			t.Errorf("%s не распакован: %v", rel, err)
		}
	}
	hook := filepath.Join(want, "hooks/require-result.sh")
	if st, err := os.Stat(hook); err != nil {
		t.Errorf("хук не распакован: %v", err)
	} else if st.Mode()&0o111 == 0 {
		t.Errorf("хук с shebang не исполняемый: %v", st.Mode())
	}

	edited := filepath.Join(want, "workflow.yaml")
	if err := os.WriteFile(edited, []byte("правка руками\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o2, err := ResolveOffice(Resolve{Unpack: true})
	if err != nil {
		t.Fatalf("ResolveOffice повторно: %v", err)
	}
	if o2 != o {
		t.Errorf("повторный Office = %+v, ждали тот же %+v", o2, o)
	}
	if got, err := os.ReadFile(edited); err != nil || string(got) != "правка руками\n" {
		t.Errorf("правка в распакованном офисе не сохранена: %q, %v", got, err)
	}
	if d := o.Describe(); d != "v0.7.0 → "+want {
		t.Errorf("Describe() = %q, ждали %q", d, "v0.7.0 → "+want)
	}
}

func TestResolveOfficeWithoutUnpackOnlyComputes(t *testing.T) {
	home := payloadHome(t)
	fakePayload(t)
	releaseVersion(t, "v0.7.0")
	o, err := ResolveOffice(Resolve{})
	if err != nil {
		t.Fatalf("ResolveOffice: %v", err)
	}
	if want := filepath.Join(home, OfficeDir, "v0.7.0"); o.Root != want {
		t.Errorf("Root = %q, ждали %q", o.Root, want)
	}
	absent(t, filepath.Join(home, OfficeDir))
}

func TestResolveOfficeBuildInfoRevision(t *testing.T) {
	home := payloadHome(t)
	fakePayload(t)
	releaseVersion(t, "")
	buildInfo(t, rev, false)
	o, err := ResolveOffice(Resolve{})
	if err != nil {
		t.Fatalf("ResolveOffice: %v", err)
	}
	if o.Identity != rev {
		t.Errorf("Identity = %q, ждали %q", o.Identity, rev)
	}
	if want := filepath.Join(home, OfficeDir, rev[:12]); o.Root != want {
		t.Errorf("Root = %q, ждали %q", o.Root, want)
	}
}

// Грязная сборка: личность — commit-dirty, а каталог ещё и по содержимому:
// другая поставка того же commit — другой каталог, и ни одна не переписывается.
func TestResolveOfficeDirtyBuildKeysDirByContent(t *testing.T) {
	home := payloadHome(t)
	m := fakePayload(t)
	releaseVersion(t, "")
	buildInfo(t, rev, true)
	o1, err := ResolveOffice(Resolve{})
	if err != nil {
		t.Fatalf("ResolveOffice: %v", err)
	}
	if o1.Identity != rev+"-dirty" {
		t.Errorf("Identity = %q, ждали %q", o1.Identity, rev+"-dirty")
	}
	dirPattern := regexp.MustCompile(`^` + regexp.QuoteMeta(rev[:12]) + `-dirty-[0-9a-f]{8}$`)
	if base := filepath.Base(o1.Root); !dirPattern.MatchString(base) {
		t.Errorf("каталог %q не по образцу %s", base, dirPattern)
	}
	if dir, want := filepath.Dir(o1.Root), filepath.Join(home, OfficeDir); dir != want {
		t.Errorf("каталог офисов = %q, ждали %q", dir, want)
	}
	m["workflow.yaml"] = &fstest.MapFile{Data: []byte("другое\n")}
	o2, err := ResolveOffice(Resolve{})
	if err != nil {
		t.Fatalf("ResolveOffice с другой поставкой: %v", err)
	}
	if o2.Identity != o1.Identity {
		t.Errorf("Identity изменилась: %q → %q", o1.Identity, o2.Identity)
	}
	if o2.Root == o1.Root {
		t.Errorf("другая поставка того же commit легла в тот же каталог %q", o2.Root)
	}
}

func TestResolveOfficeRefusesWithoutIdentity(t *testing.T) {
	for name, settings := range map[string]func(){
		"нет build info": func() { buildInfo(t, "", false) },
		"нет vcs.revision": func() {
			prev := readBuildInfo
			readBuildInfo = func() (*debug.BuildInfo, bool) { return &debug.BuildInfo{}, true }
			t.Cleanup(func() { readBuildInfo = prev })
		},
	} {
		t.Run(name, func(t *testing.T) {
			home := payloadHome(t)
			fakePayload(t)
			releaseVersion(t, "")
			settings()
			_, err := ResolveOffice(Resolve{Unpack: true})
			if err == nil {
				t.Fatal("ResolveOffice не отказал")
			}
			for _, want := range []string{"без личности", ConfigRootEnv} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("ошибка %q без %q", err, want)
				}
			}
			absent(t, filepath.Join(home, OfficeDir))
		})
	}
}
