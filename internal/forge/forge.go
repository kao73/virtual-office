// Package forge — где живут pull request проекта.
//
// Офис открывает PR, следит за ним и убирает за собой; сливает по умолчанию
// человек, а на проекте с явным auto_merge.enabled — офис сам, детерминированным
// кодом, тем же интерфейсом Merge.
//
// Слияемость ветки forge не спрашивают. Она считается локально, по bare-клону
// (`git merge-tree`), и это не оптимизация: так проверка не зависит ни от сети,
// ни от реализации forge, а второй forge обходится дешевле — интерфейс у него
// из трёх методов.
package forge

import (
	"errors"
	"fmt"
	"strings"
)

// State — состояние pull request глазами офиса.
type State string

const (
	// Open — PR открыт: человек ещё не решил.
	Open State = "open"
	// Merged — PR слит: работа в ветке по умолчанию.
	Merged State = "merged"
	// Closed — PR закрыт без слияния: работу не взяли.
	Closed State = "closed"
)

// ErrRefused — forge отказался открывать pull request, и это его окончательный
// ответ, а не сбой связи.
//
// Различать их обязательно. Отказ (ветки нет, коммитов нет, PR уже открыт) —
// аномалия задачи, и разговаривать о ней надо с человеком. Сбой связи — беда
// обвязки, и задачу за него двигать нельзя: следующий проход попробует снова.
var ErrRefused = errors.New("forge отказался открывать pull request")

// Forge — то немногое, что офису нужно от хостинга репозиториев.
//
// Проект называется ключом трекера: какой репозиторий за ним стоит, forge
// выясняет сам — из того же repo_url, которым пользуется раннер. Второго места
// для этого факта нет намеренно: два места однажды разойдутся.
type Forge interface {
	// OpenPR открывает pull request ветки задачи в ветку по умолчанию
	// и возвращает его адрес.
	OpenPR(project, branch, base, title, body string) (string, error)
	// PRState отвечает, что стало с pull request по его адресу.
	PRState(url string) (State, error)
	// Merge сливает pull request. ErrRefused — окончательный отказ forge
	// (не мержится, права нет, PR не найден); прочая ошибка — сбой связи,
	// задачу за неё двигать нельзя.
	Merge(url string) error
}

// Repo — координаты репозитория на forge.
type Repo struct {
	Owner string
	Name  string
}

// ParseRepo достаёт владельца и имя из repo_url проекта.
//
// Форм у адреса три, и все три встречаются в живых конфигурациях:
// https://host/owner/name.git, git@host:owner/name.git и то же самое без .git.
func ParseRepo(repoURL string) (Repo, error) {
	path := strings.TrimSpace(repoURL)
	if scheme := strings.Index(path, "://"); scheme >= 0 {
		path = path[scheme+len("://"):]
		if slash := strings.Index(path, "/"); slash >= 0 {
			path = path[slash+1:]
		}
	} else if at := strings.Index(path, "@"); at >= 0 {
		// git@github.com:owner/name.git
		if colon := strings.Index(path[at:], ":"); colon >= 0 {
			path = path[at+colon+1:]
		}
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")

	owner, name, found := strings.Cut(path, "/")
	if !found || owner == "" || name == "" || strings.Contains(name, "/") {
		return Repo{}, fmt.Errorf("из repo_url %q не выведены владелец и имя репозитория", repoURL)
	}
	return Repo{Owner: owner, Name: name}, nil
}

// String — owner/name, как репозиторий зовут люди и REST API.
func (r Repo) String() string { return r.Owner + "/" + r.Name }
