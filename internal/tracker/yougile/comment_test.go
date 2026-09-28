package yougile

import (
	"errors"
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
