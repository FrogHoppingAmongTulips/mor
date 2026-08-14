package webauth

import (
	"os"
	"path/filepath"
	"testing"
)

func tokensAt(t *testing.T) (*Tokens, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tokens.json")
	return OpenTokens(path), path
}

func TestIssuedTokenIsAccepted(t *testing.T) {
	tk, _ := tokensAt(t)
	secret, err := tk.Issue("бот")
	if err != nil {
		t.Fatal(err)
	}
	if !tk.Valid(secret) {
		t.Fatal("выданный токен не принят")
	}
}

func TestStrangerTokenIsRejected(t *testing.T) {
	tk, _ := tokensAt(t)
	if _, err := tk.Issue("бот"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "mor_", "mor_0000", "не токен"} {
		if tk.Valid(bad) {
			t.Fatalf("принят чужой токен %q", bad)
		}
	}
}

// The whole point of a token is that it can be taken away on its own.
func TestRevokedTokenStopsWorking(t *testing.T) {
	tk, path := tokensAt(t)
	secret, _ := tk.Issue("бот")
	other, _ := tk.Issue("скрипт")

	if !tk.Revoke("бот") {
		t.Fatal("отзыв не сработал")
	}
	if tk.Valid(secret) {
		t.Fatal("отозванный токен всё ещё принимается")
	}
	if !tk.Valid(other) {
		t.Fatal("отзыв задел чужой токен")
	}
	if OpenTokens(path).Valid(secret) {
		t.Fatal("отозванный токен вернулся после перезапуска")
	}
}

func TestRevokeUnknownSaysSo(t *testing.T) {
	tk, _ := tokensAt(t)
	if tk.Revoke("нет такого") {
		t.Fatal("отчитался об отзыве несуществующего токена")
	}
}

func TestTokensSurviveRestart(t *testing.T) {
	tk, path := tokensAt(t)
	secret, _ := tk.Issue("бот")
	if !OpenTokens(path).Valid(secret) {
		t.Fatal("токен не пережил перезапуск")
	}
}

// Only the hash is stored: a token readable out of the file would be a second
// password lying on the disk in the clear.
func TestFileHoldsNoUsableSecret(t *testing.T) {
	tk, path := tokensAt(t)
	secret, _ := tk.Issue("бот")

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) == "" {
		t.Fatal("файл пуст")
	}
	if contains(string(b), secret) {
		t.Fatal("сам токен лежит в файле открытым текстом")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("права %04o, нужно 0600", fi.Mode().Perm())
	}
}

func TestListHidesHashes(t *testing.T) {
	tk, _ := tokensAt(t)
	if _, err := tk.Issue("бот"); err != nil {
		t.Fatal(err)
	}
	for _, item := range tk.List() {
		if item.Hash != "" {
			t.Fatal("список отдаёт хеш")
		}
		if item.Name != "бот" {
			t.Fatalf("имя %q", item.Name)
		}
	}
}

func TestDamagedFileDoesNotLockTheOwnerOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	if err := os.WriteFile(path, []byte("{не json"), 0o600); err != nil {
		t.Fatal(err)
	}
	tk := OpenTokens(path)
	secret, err := tk.Issue("бот")
	if err != nil {
		t.Fatal(err)
	}
	if !tk.Valid(secret) {
		t.Fatal("после повреждённого файла токены не работают")
	}
}

func contains(hay, needle string) bool {
	return needle != "" && len(hay) >= len(needle) && indexOf(hay, needle) >= 0
}

func indexOf(hay, needle string) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// A token is issued from the command line while the panel is already running.
// The panel must accept it without a restart.
func TestTokenIssuedByAnotherProcessWorksAtOnce(t *testing.T) {
	path := t.TempDir() + "/tokens.json"
	panel := OpenTokens(path) // running server, file does not exist yet

	cli := OpenTokens(path) // separate `mor token new`
	secret, err := cli.Issue("бот")
	if err != nil {
		t.Fatal(err)
	}

	if !panel.Valid(secret) {
		t.Fatal("панель не приняла только что выпущенный токен")
	}

	if !cli.Revoke("бот") {
		t.Fatal("токен не отозвался")
	}
	if panel.Valid(secret) {
		t.Fatal("отозванный токен всё ещё работает")
	}
}
