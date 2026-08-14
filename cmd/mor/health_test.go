package main

import "testing"

func TestWorstPicksMostSevere(t *testing.T) {
	list := []problem{
		{text: "диск занят на 91%", level: levelWarn},
		{text: "не смог проверить часы", level: levelUnknown},
		{text: "DNS не отвечает", level: levelBad},
	}
	if got := worst(list); got != "DNS не отвечает" {
		t.Errorf("worst = %q, ждали поломку", got)
	}
	if got := worst(list[:2]); got != "диск занят на 91%" {
		t.Errorf("без поломки должно показываться предупреждение, получили %q", got)
	}
	if got := worst(nil); got != "" {
		t.Errorf("исправный сервер должен молчать, получили %q", got)
	}
}

// Тишина означает «проверено и в порядке». Поэтому проверка, которая не
// отработала, обязана сказать о себе — иначе её молчание читается как «хорошо».
func TestUnknownIsNotSilence(t *testing.T) {
	if got := worst([]problem{{text: "не смог проверить место на диске", level: levelUnknown}}); got == "" {
		t.Error("непроверенное молчит так же, как исправное")
	}
}

func TestParseUfw(t *testing.T) {
	out := `Status: active

To                         Action      From
--                         ------      ----
2096/udp                   ALLOW       Anywhere
443/tcp                    ALLOW       Anywhere
8880/tcp                   ALLOW       Anywhere
2096/udp (v6)              ALLOW       Anywhere (v6)
22                         ALLOW       Anywhere
`
	open := parseUfw(out)
	for _, p := range []string{"2096/udp", "443/tcp", "8880/tcp"} {
		if !open[p] {
			t.Errorf("порт %s не найден в выводе ufw", p)
		}
	}
	if open["Status:"] || open["To"] || open["22"] {
		t.Errorf("в набор попал мусор из шапки: %v", open)
	}
}

// «Status: inactive» содержит слово active. Поиск подстроки превращал сервер
// без firewall в сервер, где закрыты все порты.
func TestUfwInactiveIsNotActive(t *testing.T) {
	if ufwActive("Status: inactive\n") {
		t.Error("выключенный ufw принят за включённый")
	}
	if !ufwActive("Status: active\n\nTo    Action    From\n") {
		t.Error("включённый ufw не распознан")
	}
	if ufwActive("") {
		t.Error("пустой вывод принят за включённый firewall")
	}
	if ufwActive("ERROR: You need to be root to run this script\n") {
		t.Error("сообщение об ошибке принято за включённый firewall")
	}
}

func TestParseFirewalld(t *testing.T) {
	open := parseFirewalld("2096/udp 443/tcp 8880/tcp\n")
	if len(open) != 3 || !open["443/tcp"] {
		t.Errorf("порты firewalld разобраны неверно: %v", open)
	}
}

func TestParseDf(t *testing.T) {
	header := "Filesystem     1024-blocks     Used Available Capacity Mounted on\n"

	if p := parseDf(header + "/dev/vda1  41152736 12000000  27000000      31% /\n"); p != nil {
		t.Errorf("свободный диск не должен ничего сообщать: %+v", p)
	}

	p := parseDf(header + "/dev/vda1  41152736 40000000     50000      99% /\n")
	if p == nil || p.level != levelBad {
		t.Errorf("почти полный диск должен быть поломкой: %+v", p)
	}

	p = parseDf(header + "/dev/vda1  41152736 37000000   4000000      92% /\n")
	if p == nil || p.level != levelWarn {
		t.Errorf("диск на 92%% должен быть предупреждением: %+v", p)
	}

	for _, junk := range []string{"", "мусор", header} {
		if p := parseDf(junk); p == nil || p.level != levelUnknown {
			t.Errorf("непонятный вывод df должен давать «не смог проверить», получили %+v", p)
		}
	}
}
