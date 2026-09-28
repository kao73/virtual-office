package yougile

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
)

const markedBody = "[office run:r1 role:analyst outcome:question]\nЧто делать с <b>тегами</b> & амперсандом?\n\n## Вопросы\n1. да/нет"

func TestCommentPostsVerbatimTextAndEscapedHTML(t *testing.T) {
	tr, fake := fixture(t)
	if err := tr.Comment(testKey, tracker.BySystem(), markedBody); err != nil {
		t.Fatal(err)
	}
	post := fake.chatPosts[0]
	if post["text"] != markedBody {
		t.Errorf("text искажён: %q", post["text"])
	}
	wantHTML := "[office run:r1 role:analyst outcome:question]<br>Что делать с &lt;b&gt;тегами&lt;/b&gt; &amp; амперсандом?<br><br>## Вопросы<br>1. да/нет"
	if post["textHtml"] != wantHTML {
		t.Errorf("textHtml = %q", post["textHtml"])
	}
	if post["label"] != "" {
		t.Errorf("label = %#v, ожидалась пустая строка (поле обязательно)", post["label"])
	}
}

// Spec: комментарий читается обратно дословно; автор — та же учётка, что Whoami.
func TestCommentRoundTripsAndIsAttributedToOffice(t *testing.T) {
	tr, _ := fixture(t)
	if err := tr.Comment(testKey, tracker.BySystem(), markedBody); err != nil {
		t.Fatal(err)
	}
	task, err := tr.Get(testKey)
	if err != nil {
		t.Fatal(err)
	}
	who, _ := tr.Whoami()
	last := task.Comments[len(task.Comments)-1]
	if last.Body != markedBody || last.Author != who {
		t.Errorf("прочитано обратно: %+v (Whoami=%q)", last, who)
	}
}

func TestCommentFollowsOwnership(t *testing.T) {
	tr, fake := fixture(t)
	fake.setLease(testKey, "run-1", now.Add(time.Minute))
	if err := tr.Comment(testKey, tracker.BySystem(), "x"); !errors.Is(err, tracker.ErrNotOwner) {
		t.Errorf("система поверх живой аренды дала %v", err)
	}
	if len(fake.chatPosts) != 0 {
		t.Error("комментарий ушёл без права")
	}
	if err := tr.Comment(testKey, tracker.ByRun("run-1"), "x"); err != nil {
		t.Errorf("владелец не смог написать: %v", err)
	}
}

func TestFindByMarkerFindsLabeledTask(t *testing.T) {
	tr, _ := fixture(t)
	want, err := tr.CreateTask(testProject, tracker.TaskInput{Summary: "Child", Labels: []string{"split:P:a"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.CreateTask(testProject, tracker.TaskInput{Summary: "Other", Labels: []string{"split:P:b"}}); err != nil {
		t.Fatal(err)
	}
	refs, err := tr.FindByMarker(testProject, "split:P:a")
	if err != nil || len(refs) != 1 || refs[0].Key != want.Key {
		t.Errorf("FindByMarker = %+v, %v", refs, err)
	}
}

// Ребёнка уже унесли дальше по графу — метка всё равно находит его.
func TestFindByMarkerSearchesEveryStatusColumn(t *testing.T) {
	tr, fake := fixture(t)
	fake.addTask(&fakeTask{ID: "moved", ColumnID: colReview, Timestamp: now.UnixMilli(),
		APIData: map[string]any{"labels": []any{"split:P:a"}}})
	refs, err := tr.FindByMarker(testProject, "split:P:a")
	if err != nil || !slices.Equal(keys(refs), []string{"moved"}) {
		t.Errorf("FindByMarker = %v, %v", keys(refs), err)
	}
}

// Review Focus #1: найденная метка в колонке вне графа — громкая ошибка,
// а не «не найдено», которое кончилось бы дублем ребёнка.
func TestFindByMarkerFailsLoudOnUnmappedColumn(t *testing.T) {
	tr, fake := fixture(t)
	fake.addTask(&fakeTask{ID: "parked", ColumnID: colOutside, Timestamp: now.UnixMilli(),
		APIData: map[string]any{"labels": []any{"split:P:a"}}})
	_, err := tr.FindByMarker(testProject, "split:P:a")
	if !errors.Is(err, ErrUnmappedColumn) || !strings.Contains(err.Error(), "parked") {
		t.Errorf("метка вне графа дала %v", err)
	}
}

func TestFindByMarkerEmptyWhenNothingMatches(t *testing.T) {
	tr, _ := fixture(t)
	refs, err := tr.FindByMarker(testProject, "split:none")
	if err != nil || len(refs) != 0 {
		t.Errorf("FindByMarker = %+v, %v", refs, err)
	}
}

// Метку в тексте комментария FindByMarker не ищет: контракт — метки задачи
// (pipeline.ensureChildren кладёт её в TaskInput.Labels).
func TestFindByMarkerIgnoresCommentText(t *testing.T) {
	tr, fake := fixture(t)
	fake.messages[testKey] = []fakeMessage{{ID: 1, From: officeUserID, Text: "split:P:a"}}
	refs, err := tr.FindByMarker(testProject, "split:P:a")
	if err != nil || len(refs) != 0 {
		t.Errorf("FindByMarker по тексту комментария: %+v, %v", refs, err)
	}
}

func TestFindByMarkerUnknownProject(t *testing.T) {
	tr, _ := fixture(t)
	if _, err := tr.FindByMarker("OTHER", "m"); !errors.Is(err, tracker.ErrNoProject) {
		t.Errorf("чужой проект дал %v", err)
	}
}

// Метка сравнивается целиком: split:P:a — не часть split:P:ab.
func TestFindByMarkerMatchesWholeLabel(t *testing.T) {
	tr, fake := fixture(t)
	fake.addTask(&fakeTask{ID: "longer", ColumnID: colReady, Timestamp: now.UnixMilli(),
		APIData: map[string]any{"labels": []any{"split:P:ab"}}})
	fake.addTask(&fakeTask{ID: "exact", ColumnID: colReady, Timestamp: now.UnixMilli() + 1,
		APIData: map[string]any{"labels": []any{"split:P:a"}}})
	refs, err := tr.FindByMarker(testProject, "split:P:a")
	if err != nil || !slices.Equal(keys(refs), []string{"exact"}) {
		t.Errorf("FindByMarker = %v, %v", keys(refs), err)
	}
}
