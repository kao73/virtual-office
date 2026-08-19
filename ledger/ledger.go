// Package ledger — учёт прогонов: строка на прогон, дописанная в конец файла.
//
// Учёт и ограничение — разные вещи. Реестр ведётся всегда и ничего не решает:
// он отвечает на вопрос «во что это обошлось», а не «брать ли задачу». Решения
// принимает политика поверх него (пакет budget), и её может не быть вовсе.
//
// **Реестр локален для машины.** Он лежит в хозяйстве раннера, а хозяйство
// у каждой машины своё; два раннера на один проект видят каждый свою половину
// расхода. Это принятое ограничение, а не недосмотр: общий реестр означал бы
// общее хранилище, а единственное общее хранилище офиса — трекер, и класть
// в него строку на каждый прогон значило бы засорять переписку человека
// бухгалтерией. Цена прогона в тикет всё же попадает — строкой в отчёте, —
// так что посчитать задачу по переписке можно и без реестра.
package ledger

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/kao73/virtual-office/runner"
)

// FileName — имя реестра в хозяйстве раннера.
const FileName = "ledger.jsonl"

// Entry — строка реестра: один прогон.
//
// Ключ задачи и проект необязательны: ручной `run-agent` бежит без трекера,
// и требовать от него выдуманный ключ значило бы портить учёт ради схемы.
type Entry struct {
	RunID   string    `json:"run_id"`
	Task    string    `json:"task,omitempty"`
	Role    string    `json:"role"`
	Project string    `json:"project,omitempty"`
	Started time.Time `json:"started"`
	// Usage лежит плоско: cost_usd, duration_ms и turns — поля самой строки.
	// Читать реестр глазами и грепом придётся, а вложенный объект этому мешает.
	runner.Usage
	Outcome string `json:"outcome"`
	// Termination — чем прогон кончился глазами раннера: completed, truncated,
	// not_started, errored. Рядом с исходом, а не вместо него: исход — слова
	// агента, а это наблюдение. У прогона без результата исход синтетический
	// и врёт, и отличить такую строку от настоящего провала можно только здесь.
	//
	// По этим же значениям подбирается max_turns роли: сколько прогонов резалось
	// пределом, видно в сводке, а не вслепую.
	Termination string `json:"termination,omitempty"`
	ConfigSHA   string `json:"config_sha"`
	// Overrides — строка переигрыша: раннер не принял исход агента, и здесь
	// записан эффективный. Прогоном она не считается вовсе и расхода не несёт
	// (`cost_usd` нулевой): прогон был один, и заплачено за него один раз.
	// Строкой, а не правкой прежней: реестр дописывается в конец и не правится
	// никогда — иначе два раннера на машине затирали бы друг друга.
	Overrides bool `json:"overrides,omitempty"`
}

// Ledger — файл реестра.
type Ledger struct{ Path string }

// New — реестр по явному пути.
func New(path string) *Ledger { return &Ledger{Path: path} }

// Default — реестр в хозяйстве раннера: ${OFFICE_HOME}/ledger.jsonl.
func Default() (*Ledger, error) {
	home, err := runner.Home()
	if err != nil {
		return nil, err
	}
	return New(filepath.Join(home, FileName)), nil
}

// Append дописывает строку в конец реестра.
//
// Открытие с O_APPEND — не мелочь: раннеров на машине бывает несколько, и без
// него два процесса писали бы каждый по своему смещению, затирая друг друга.
// С ним ядро само ставит запись в конец файла, а строка в один вызов Write
// не разрывается пополам.
func (l *Ledger) Append(e Entry) error {
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("строка реестра не сериализована: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o755); err != nil {
		return fmt.Errorf("каталог реестра не создан: %w", err)
	}

	file, err := os.OpenFile(l.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("реестр не открыт: %w", err)
	}
	defer func() { _ = file.Close() }()

	if _, err := file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("строка реестра не записана: %w", err)
	}
	return nil
}

// Filter — что считать. Пустое поле не ограничивает ничего.
type Filter struct {
	Task  string
	Role  string
	Since time.Time
}

func (f Filter) match(e Entry) bool {
	switch {
	case f.Task != "" && e.Task != f.Task:
		return false
	case f.Role != "" && e.Role != f.Role:
		return false
	case !f.Since.IsZero() && e.Started.Before(f.Since):
		return false
	}
	return true
}

// Total — сводка по отобранным прогонам.
type Total struct {
	Runs      int
	CostUSD   float64
	ByOutcome map[string]int
	// ByTermination — чем прогоны кончались глазами раннера. Рядом с исходами,
	// а не вместо них: исход — слова агента, и у прогона без результата он
	// синтетический. Отличить усечение от настоящего провала можно только здесь,
	// а по числу усечений и подбирается max_turns роли — не вслепую.
	//
	// Обычный прогон в счёт не идёт: строка «completed столько-то» повторяла бы
	// число прогонов и ничего не сообщала.
	ByTermination map[string]int
	// Unknown — прогоны, не назвавшие цены: убитые на середине. Считаются
	// отдельно, потому что сумма без них занижена, и знать об этом обязан
	// и человек, и лимит.
	Unknown int
	// Broken — строки, которые не разобрались. Реестр дописывают на живой
	// машине, и последняя строка бывает половиной JSON.
	Broken int
}

// Average — средняя цена прогона. Прогоны без цены в среднее не идут: делить
// сумму на них значило бы занижать среднее ровно на их долю.
func (t Total) Average() float64 {
	if known := t.Runs - t.Unknown; known > 0 {
		return t.CostUSD / float64(known)
	}
	return 0
}

// Sum читает реестр и складывает отобранные строки.
//
// Отсутствие файла — пустая сводка, а не ошибка: на машине, где ещё не было
// прогонов, реестра нет и быть не должно.
//
// Нечитаемая строка не роняет чтение, но и не пропадает молча: она попадает
// в Broken. Иначе испорченный хвост занижал бы сумму, а на суммы смотрят лимиты —
// и лимит, тихо переставший срабатывать, хуже отсутствующего.
//
// Строка переигрыша прогоном не считается: она подменяет исход своего прогона
// и не добавляет ни к числу прогонов, ни к сумме. Иначе один прогон стоил бы
// в сводке двух, а исходы считались бы дважды — и агентский, и эффективный.
func (l *Ledger) Sum(f Filter) (Total, error) {
	total := Total{ByOutcome: map[string]int{}, ByTermination: map[string]int{}}
	// Исход, засчитанный прогону: строка переигрыша идёт после своего прогона
	// и заменяет его исход на эффективный.
	counted := map[string]string{}

	file, err := os.Open(l.Path)
	if errors.Is(err, os.ErrNotExist) {
		return total, nil
	}
	if err != nil {
		return total, fmt.Errorf("реестр не прочитан: %w", err)
	}
	defer func() { _ = file.Close() }()

	lines := bufio.NewScanner(file)
	// Строка реестра короткая, но задел от буфера по умолчанию не лишний:
	// упереться в предел здесь значило бы объявить сломанным целый файл.
	lines.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for lines.Scan() {
		raw := lines.Bytes()
		if len(raw) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(raw, &e); err != nil {
			total.Broken++
			continue
		}
		if !f.match(e) {
			continue
		}
		if e.Overrides {
			// Прогона в сводке может и не быть: реестр читают с любого места,
			// а начало файла бывает обрезано. Переигрыш без прогона молча
			// пропускается — выдумывать за него прогон нечестно.
			if was, found := counted[e.RunID]; found {
				total.ByOutcome[was]--
				if total.ByOutcome[was] == 0 {
					delete(total.ByOutcome, was)
				}
				total.ByOutcome[e.Outcome]++
				counted[e.RunID] = e.Outcome
			}
			continue
		}
		total.Runs++
		total.CostUSD += e.CostUSD
		total.ByOutcome[e.Outcome]++
		if e.Termination != "" && e.Termination != string(runner.TerminationCompleted) {
			total.ByTermination[e.Termination]++
		}
		counted[e.RunID] = e.Outcome
		if !e.Known() {
			total.Unknown++
		}
	}
	if err := lines.Err(); err != nil && !errors.Is(err, io.EOF) {
		return total, fmt.Errorf("реестр не дочитан: %w", err)
	}
	return total, nil
}
