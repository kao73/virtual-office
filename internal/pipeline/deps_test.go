package pipeline

import (
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/tracker"
)

func terminalIsDone(status string) bool { return status == "Done" }

func TestUnmetDependenciesNoneWhenAllTerminal(t *testing.T) {
	ref := tracker.TaskRef{Key: "OFF-2", DependsOn: []string{"OFF-1"}}
	byKey := map[string]tracker.TaskRef{"OFF-1": {Key: "OFF-1", Status: "Done"}}

	if unmet := UnmetDependencies(ref, byKey, terminalIsDone); len(unmet) != 0 {
		t.Errorf("зависимость терминальна, но гейт видит незакрытой: %+v", unmet)
	}
}

func TestUnmetDependenciesReturnsNonTerminal(t *testing.T) {
	ref := tracker.TaskRef{Key: "OFF-2", DependsOn: []string{"OFF-1"}}
	byKey := map[string]tracker.TaskRef{"OFF-1": {Key: "OFF-1", Status: "Review"}}

	unmet := UnmetDependencies(ref, byKey, terminalIsDone)
	if len(unmet) != 1 || unmet[0].Key != "OFF-1" || unmet[0].Status != "Review" {
		t.Errorf("незакрытая зависимость не найдена: %+v", unmet)
	}
}

// TestUnmetDependenciesTreatsMissingKeyAsUnresolved — зависимость,
// которой нет в byKey (удалена, никогда не существовала), не должна
// считаться свободной. Возвращается TaskRef{Key: "OFF-404"} — тот самый
// ключ, который искали, а не нулевое значение: оператору нужно видеть,
// ЧЕГО не хватает, а не только что чего-то не хватает (fix round 1,
// Finding 2 — зеркально предыдущей версии, которая отдавала byKey[key]
// как есть и теряла ключ в нулевом значении).
func TestUnmetDependenciesTreatsMissingKeyAsUnresolved(t *testing.T) {
	ref := tracker.TaskRef{Key: "OFF-2", DependsOn: []string{"OFF-404"}}
	byKey := map[string]tracker.TaskRef{}

	unmet := UnmetDependencies(ref, byKey, terminalIsDone)
	if len(unmet) != 1 {
		t.Fatalf("отсутствующая зависимость не считается незакрытой: %+v", unmet)
	}
	if unmet[0].Key != "OFF-404" {
		t.Errorf("ожидался TaskRef{Key: \"OFF-404\"} для отсутствующей зависимости, получено %+v", unmet[0])
	}
	if unmet[0].Status != "" {
		t.Errorf("у отсутствующей зависимости не должно быть статуса, получено %+v", unmet[0])
	}
}

// TestDescribeUnmetNamesMissingDependencyKey — describeUnmet обязан
// показать ключ отсутствующей зависимости, а не только факт, что
// что-то пропало: "неизвестная задача ()" не говорит оператору, какую
// задачу заводить или искать (fix round 1, Finding 2).
func TestDescribeUnmetNamesMissingDependencyKey(t *testing.T) {
	got := describeUnmet([]tracker.TaskRef{{Key: "OFF-404"}})
	want := "OFF-404 ()"
	if got != want {
		t.Errorf("describeUnmet = %q, ожидалось %q", got, want)
	}
}

func TestUnmetDependenciesEmptyWhenNoDependsOn(t *testing.T) {
	ref := tracker.TaskRef{Key: "OFF-1"}
	if unmet := UnmetDependencies(ref, nil, terminalIsDone); unmet != nil {
		t.Errorf("задача без depends_on считается заблокированной: %+v", unmet)
	}
}

func TestDescribeUnmetNamesKeyAndStatus(t *testing.T) {
	got := describeUnmet([]tracker.TaskRef{{Key: "OFF-2", Status: "Review"}, {Key: "OFF-3", Status: "InProgress"}})
	want := "OFF-2 (Review), OFF-3 (InProgress)"
	if got != want {
		t.Errorf("describeUnmet = %q, ожидалось %q", got, want)
	}
}

func TestDescribeUnmetHandlesMissingDependency(t *testing.T) {
	got := describeUnmet([]tracker.TaskRef{{}})
	if !strings.Contains(got, "неизвестная") {
		t.Errorf("describeUnmet не сообщает о пропавшей зависимости: %q", got)
	}
}
