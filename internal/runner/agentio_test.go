package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workdirWith собирает временный workdir с готовым result.json.
func workdirWith(t *testing.T, content string) string {
	t.Helper()
	workdir := t.TempDir()
	agentDir := filepath.Join(workdir, Dir)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("не создан каталог обмена: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, FileResult), []byte(content), 0o644); err != nil {
		t.Fatalf("не записан result.json: %v", err)
	}
	return workdir
}

func TestReadResultAccepts(t *testing.T) {
	cases := map[string]struct {
		content string
		want    Result
	}{
		"выполнено": {
			content: `{
			  "outcome": "done",
			  "summary": "Добавил hello.py и тест, pytest зелёный.",
			  "artifacts": ["hello.py", "a1b2c3d"],
			  "next_owner": "none"
			}`,
			want: Result{
				Outcome:   OutcomeDone,
				Summary:   "Добавил hello.py и тест, pytest зелёный.",
				Artifacts: []string{"hello.py", "a1b2c3d"},
				NextOwner: "none",
			},
		},
		"нужен человек": {
			content: `{
			  "outcome": "needs_human",
			  "summary": "Задача допускает два прочтения.",
			  "questions": [{"id": "Q1", "text": "Какую платёжную систему?",
			    "options": [{"id": "a", "label": "Stripe"}, {"id": "b", "label": "ЮKassa"}]}],
			  "next_owner": "human"
			}`,
			want: Result{
				Outcome: OutcomeNeedsHuman,
				Summary: "Задача допускает два прочтения.",
				Questions: []Question{{ID: "Q1", Text: "Какую платёжную систему?", Options: []Option{
					{ID: "a", Label: "Stripe"}, {ID: "b", Label: "ЮKassa"},
				}}},
				NextOwner: "human",
			},
		},
		"вопрос без вариантов ответа": {
			content: `{
			  "outcome": "needs_human",
			  "summary": "Нужен выбор.",
			  "questions": [{"id": "Q1", "text": "Куда деплоим?"}],
			  "next_owner": "human"
			}`,
			want: Result{
				Outcome:   OutcomeNeedsHuman,
				Summary:   "Нужен выбор.",
				Questions: []Question{{ID: "Q1", Text: "Куда деплоим?"}},
				NextOwner: "human",
			},
		},
		"заблокировано": {
			content: `{
			  "outcome": "blocked",
			  "summary": "Нет доступа к стенду.",
			  "blocker": "закрыт сетевой доступ к staging",
			  "next_owner": "human"
			}`,
			want: Result{
				Outcome:   OutcomeBlocked,
				Summary:   "Нет доступа к стенду.",
				Blocker:   "закрыт сетевой доступ к staging",
				NextOwner: "human",
			},
		},
		"разбить на подзадачи": {
			content: `{
			  "outcome": "split",
			  "summary": "Постановка описывает две независимые сущности.",
			  "questions": [{"id": "Q1", "text": "Разбить на 2, как предложено?"}],
			  "split": {"children": [
			    {"id": "category-crud", "title": "Category CRUD", "description": "..."},
			    {"id": "transaction-crud", "title": "Transaction CRUD", "description": "...", "depends_on": ["category-crud"]}
			  ]},
			  "next_owner": "human"
			}`,
			want: Result{
				Outcome:   OutcomeSplit,
				Summary:   "Постановка описывает две независимые сущности.",
				Questions: []Question{{ID: "Q1", Text: "Разбить на 2, как предложено?"}},
				Split: &Split{Children: []SplitChild{
					{ID: "category-crud", Title: "Category CRUD", Description: "..."},
					{ID: "transaction-crud", Title: "Transaction CRUD", Description: "...", DependsOn: []string{"category-crud"}},
				}},
				NextOwner: "human",
			},
		},
		"передача другой роли": {
			content: `{
			  "outcome": "done",
			  "summary": "Реализовано, нужен ревью.",
			  "next_owner": "reviewer"
			}`,
			want: Result{
				Outcome:   OutcomeDone,
				Summary:   "Реализовано, нужен ревью.",
				NextOwner: "reviewer",
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ReadResult(workdirWith(t, tc.content))
			if err != nil {
				t.Fatalf("контракт соблюдён, но результат отвергнут: %v", err)
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tc.want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("разобрано не то\nполучено: %s\nожидалось: %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestReadResultRejects(t *testing.T) {
	cases := map[string]struct {
		content  string
		wantPart string
	}{
		"мусор вместо JSON": {
			content:  `не json вовсе`,
			wantPart: "не разобран",
		},
		"не объект": {
			content:  `["done"]`,
			wantPart: "не разобран",
		},
		"неизвестное поле": {
			content:  `{"outcome":"done","summary":"с.","next_owner":"none","outome":"done"}`,
			wantPart: "не разобран",
		},
		"хвост после объекта": {
			content:  `{"outcome":"done","summary":"с.","next_owner":"none"} лишнее`,
			wantPart: "лишнее содержимое",
		},
		"неизвестный исход": {
			content:  `{"outcome":"maybe","summary":"с.","next_owner":"none"}`,
			wantPart: "outcome=",
		},
		"исход не указан": {
			content:  `{"summary":"с.","next_owner":"none"}`,
			wantPart: "outcome=",
		},
		"пустой summary": {
			content:  `{"outcome":"done","summary":"   ","next_owner":"none"}`,
			wantPart: "summary пуст",
		},
		"нет next_owner": {
			content:  `{"outcome":"done","summary":"с."}`,
			wantPart: "next_owner пуст",
		},
		"мусорный next_owner": {
			content:  `{"outcome":"done","summary":"с.","next_owner":"Человек Иванов"}`,
			wantPart: "next_owner=",
		},
		"needs_human без вопросов": {
			content:  `{"outcome":"needs_human","summary":"с.","next_owner":"human"}`,
			wantPart: "questions пуст",
		},
		"вопрос без текста": {
			content:  `{"outcome":"needs_human","summary":"с.","questions":[{"id":"Q1","text":" "}],"next_owner":"human"}`,
			wantPart: "questions[0].text пуст",
		},
		// По метке человек отвечает, а раннер разбирает ответ. Метки нет или она
		// не той формы — ответить нечем; метка повторяется — ответ неадресуем.
		"вопрос без метки": {
			content:  `{"outcome":"needs_human","summary":"с.","questions":[{"text":"а?"}],"next_owner":"human"}`,
			wantPart: "questions[0].id пуст",
		},
		"метка не той формы": {
			content:  `{"outcome":"needs_human","summary":"с.","questions":[{"id":"первый","text":"а?"}],"next_owner":"human"}`,
			wantPart: "ожидается Q и число",
		},
		"метка повторяется": {
			content: `{"outcome":"needs_human","summary":"с.","questions":[` +
				`{"id":"Q1","text":"а?"},{"id":"Q1","text":"б?"}],"next_owner":"human"}`,
			wantPart: "повторяется",
		},
		// Вопрос едет в тикет строкой протокола: перевод строки внутри разрезал
		// бы её пополам, и разбор увидел бы половину.
		"вопрос в несколько строк": {
			content:  `{"outcome":"needs_human","summary":"с.","questions":[{"id":"Q1","text":"а?\nи ещё б?"}],"next_owner":"human"}`,
			wantPart: "в несколько строк",
		},
		"вариант без идентификатора": {
			content: `{"outcome":"needs_human","summary":"с.","questions":[` +
				`{"id":"Q1","text":"а?","options":[{"id":"","label":"да"}]}],"next_owner":"human"}`,
			wantPart: "options[0].id пуст",
		},
		"идентификатор варианта с пробелом": {
			content: `{"outcome":"needs_human","summary":"с.","questions":[` +
				`{"id":"Q1","text":"а?","options":[{"id":"да, конечно","label":"да"}]}],"next_owner":"human"}`,
			wantPart: "с пробелом",
		},
		"варианты с одинаковыми идентификаторами": {
			content: `{"outcome":"needs_human","summary":"с.","questions":[` +
				`{"id":"Q1","text":"а?","options":[{"id":"a","label":"да"},{"id":"A","label":"нет"}]}],"next_owner":"human"}`,
			wantPart: "options[1].id",
		},
		"вариант без подписи": {
			content: `{"outcome":"needs_human","summary":"с.","questions":[` +
				`{"id":"Q1","text":"а?","options":[{"id":"a","label":" "}]}],"next_owner":"human"}`,
			wantPart: "options[0].label пуст",
		},
		"blocked без блокера": {
			content:  `{"outcome":"blocked","summary":"с.","next_owner":"human"}`,
			wantPart: "blocker пуст",
		},
		"вопросы при done": {
			content:  `{"outcome":"done","summary":"с.","questions":[{"id":"Q1","text":"а?"}],"next_owner":"none"}`,
			wantPart: "вопросы только для needs_human",
		},
		"блокер при done": {
			content:  `{"outcome":"done","summary":"с.","blocker":"нечто","next_owner":"none"}`,
			wantPart: "блокер только для blocked",
		},
		"split без вопросов": {
			content:  `{"outcome":"split","summary":"с.","split":{"children":[{"id":"a","title":"т","description":"о"}]},"next_owner":"human"}`,
			wantPart: "questions пуст",
		},
		"split без split.children": {
			content:  `{"outcome":"split","summary":"с.","questions":[{"id":"Q1","text":"а?"}],"next_owner":"human"}`,
			wantPart: "split.children пуст",
		},
		"split с пустым children": {
			content:  `{"outcome":"split","summary":"с.","questions":[{"id":"Q1","text":"а?"}],"split":{"children":[]},"next_owner":"human"}`,
			wantPart: "split.children пуст",
		},
		"split при done": {
			content:  `{"outcome":"done","summary":"с.","split":{"children":[{"id":"a","title":"т","description":"о"}]},"next_owner":"none"}`,
			wantPart: "split только для outcome=split",
		},
		"дубль id в split.children": {
			content: `{"outcome":"split","summary":"с.","questions":[{"id":"Q1","text":"а?"}],"split":{"children":[` +
				`{"id":"a","title":"т1","description":"о1"},{"id":"a","title":"т2","description":"о2"}]},"next_owner":"human"}`,
			wantPart: `id="a" повторяется`,
		},
		"висячая ссылка в depends_on": {
			content: `{"outcome":"split","summary":"с.","questions":[{"id":"Q1","text":"а?"}],"split":{"children":[` +
				`{"id":"a","title":"т","description":"о","depends_on":["нет-такого"]}]},"next_owner":"human"}`,
			wantPart: "такого id в списке нет",
		},
		"цикл зависимостей в split.children": {
			content: `{"outcome":"split","summary":"с.","questions":[{"id":"Q1","text":"а?"}],"split":{"children":[` +
				`{"id":"a","title":"т1","description":"о1","depends_on":["b"]},` +
				`{"id":"b","title":"т2","description":"о2","depends_on":["a"]}]},"next_owner":"human"}`,
			wantPart: "образуют цикл зависимостей",
		},
		"пустой title в split.children": {
			content: `{"outcome":"split","summary":"с.","questions":[{"id":"Q1","text":"а?"}],"split":{"children":[` +
				`{"id":"a","title":" ","description":"о"}]},"next_owner":"human"}`,
			wantPart: "title пуст",
		},
		"пустой description в split.children": {
			content: `{"outcome":"split","summary":"с.","questions":[{"id":"Q1","text":"а?"}],"split":{"children":[` +
				`{"id":"a","title":"т","description":" "}]},"next_owner":"human"}`,
			wantPart: "description пуст",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ReadResult(workdirWith(t, tc.content))
			if err == nil {
				t.Fatalf("контракт нарушен, но результат принят")
			}
			if !strings.Contains(err.Error(), tc.wantPart) {
				t.Errorf("ошибка не объясняет нарушение\nполучено: %v\nожидалась подстрока: %q", err, tc.wantPart)
			}
		})
	}
}

func TestReadResultReportsAllViolationsAtOnce(t *testing.T) {
	workdir := workdirWith(t, `{"outcome":"maybe","summary":"","next_owner":""}`)

	_, err := ReadResult(workdir)
	if err == nil {
		t.Fatal("контракт нарушен трижды, но результат принят")
	}
	for _, part := range []string{"outcome=", "summary пуст", "next_owner пуст"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("в ошибке нет упоминания %q: %v", part, err)
		}
	}
}

func TestReadResultMissingFile(t *testing.T) {
	_, err := ReadResult(t.TempDir())
	if err == nil {
		t.Fatal("файла нет, но результат принят")
	}
	if !strings.Contains(err.Error(), "не прочитан") {
		t.Errorf("ошибка не объясняет отсутствие файла: %v", err)
	}
}

// Раннер не додумывает исход за агента: молчание — это failed, и такой результат
// сам обязан быть валидным по контракту.
func TestFailedResultIsValid(t *testing.T) {
	r := FailedResult("агент завершился, не оставив .agent/result.json")

	if r.Outcome != OutcomeFailed {
		t.Errorf("исход %q, ожидался failed", r.Outcome)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("синтетический результат сам нарушает контракт: %v", err)
	}
}

// Пустые необязательные поля не должны появляться в сериализованном виде:
// иначе done-результат выглядит так, будто в нём есть вопросы и блокер.
func TestResultOmitsEmptyOptionalFields(t *testing.T) {
	raw, err := json.Marshal(Result{Outcome: OutcomeDone, Summary: "с.", NextOwner: "none"})
	if err != nil {
		t.Fatalf("результат не сериализуется: %v", err)
	}
	for _, field := range []string{"details_md", "artifacts", "questions", "blocker", "split"} {
		if strings.Contains(string(raw), field) {
			t.Errorf("пустое поле %q попало в JSON: %s", field, raw)
		}
	}
}

// Ограждение получает путь к файлу результата, а не рабочую папку: команду
// собирает адаптер, и рабочей папки в ней нет. Разбор при этом обязан быть
// тем же самым — иначе хук и раннер разъедутся, как уже разъезжались.
func TestReadResultFileTakesExplicitPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "исход.json")
	if err := os.WriteFile(path, []byte(`{"outcome":"done","summary":"с.","next_owner":"none"}`), 0o644); err != nil {
		t.Fatalf("файл результата не записан: %v", err)
	}

	result, err := ReadResultFile(path)
	if err != nil {
		t.Fatalf("результат не прочитан: %v", err)
	}
	if result.Outcome != OutcomeDone {
		t.Errorf("исход %q, ожидался done", result.Outcome)
	}

	if _, err := ReadResultFile(filepath.Join(t.TempDir(), "нет.json")); err == nil {
		t.Error("отсутствующий файл прочитан без ошибки")
	}
}
