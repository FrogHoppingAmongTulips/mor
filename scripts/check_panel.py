#!/usr/bin/env python3
"""Static checks for cmd/mor/web/panel.html.

The panel is one file of markup, styles and logic that the Go compiler never
looks at. Every way it has broken so far was invisible to `go build`: a handler
whose function was deleted, a translation key with no text behind it, an inline
handler that a strict Content-Security-Policy would refuse to run. These checks
are cheap and catch exactly that class of mistake.
"""

import re
import sys
from pathlib import Path

PANEL = Path(__file__).resolve().parent.parent / "cmd" / "mor" / "web" / "panel.html"

problems: list[str] = []


def fail(msg: str) -> None:
    problems.append(msg)


src = PANEL.read_text(encoding="utf-8")
script = src[src.index("<script>") : src.rindex("</script>")]

# --- every action has a handler, every handler is used -----------------------

declared = set(re.findall(r'data-act="(\w+)"', src))
table = script[script.index("const ACTIONS") : script.index("};", script.index("const ACTIONS"))]
handled = set(re.findall(r"^\s*(\w+):", table, re.M))

for missing in sorted(declared - handled):
    fail(f'data-act="{missing}" в разметке, но обработчика нет')
for unused in sorted(handled - declared):
    fail(f"обработчик {unused} объявлен, но ни одна кнопка его не вызывает")

# --- functions referenced from the dispatcher actually exist -----------------

defined = set(re.findall(r"function\s+(\w+)\s*\(", script))
defined |= set(re.findall(r"(?:const|let|var)\s+(\w+)\s*=\s*(?:async\s*)?\(", script))
for name in sorted(set(re.findall(r"=>\s*(\w+)\(", table))):
    if name not in defined and name not in ("api", "select", "toast"):
        fail(f"обработчик вызывает {name}(), которой нет")

# --- translations ------------------------------------------------------------

block = re.search(r"const I18N=\{\s*\n(.*?)\n\};", script, re.S)
if not block:
    fail("не нашёл словарь переводов")
else:
    body = block.group(1)
    ru = body[body.index("ru:{") : body.index("en:{")]
    en = body[body.index("en:{") :]
    # String values are emptied first: "port found:" would otherwise look like
    # a key named `found`, and the check would report a phantom mismatch.
    def keys(t: str) -> list[str]:
        bare = re.sub(r"'(?:[^'\\]|\\.)*'", "''", t)
        return re.findall(r"[{,\s]([A-Za-z]\w*)\s*:", bare)
    ru_keys, en_keys = keys(ru), keys(en)

    for lang, ks in (("ru", ru_keys), ("en", en_keys)):
        dupes = sorted({k for k in ks if ks.count(k) > 1})
        for d in dupes:
            fail(f"ключ {d} объявлен в {lang} дважды — второй молча перекрывает первый")

    for only_ru in sorted(set(ru_keys) - set(en_keys)):
        fail(f"ключ {only_ru} есть в ru, но не в en")
    for only_en in sorted(set(en_keys) - set(ru_keys)):
        fail(f"ключ {only_en} есть в en, но не в ru")

    used = set(re.findall(r"t\('(\w+)'\)", script))
    used |= set(re.findall(r'data-i18n="(\w+)"', src))
    used |= set(re.findall(r'data-i18n-ph="(\w+)"', src))
    used.discard("div")  # createElement('div'), not a translation
    for missing in sorted(used - set(ru_keys)):
        fail(f"перевода для {missing} нет")

# --- nothing the Content-Security-Policy would refuse ------------------------

for m in re.finditer(r"<[^>]*\son(click|input|change|submit|load)=", src):
    fail("обработчик в разметке — строгий CSP его запретит: " + src[m.start() : m.start() + 60].strip())

for m in re.finditer(r'\sstyle="[^"]', src):
    fail("атрибут style= — строгий CSP его запретит: " + src[max(0, m.start() - 40) : m.start() + 30].strip())

# --- interactive elements must be reachable by keyboard ----------------------

for m in re.finditer(r"<(div|span)[^>]*data-act=", src):
    fail(f"<{m.group(1)}> с data-act — с клавиатуры недостижим, нужен <button>")

# --- the two inline blocks the CSP pins by hash must exist -------------------

if src.count("<script>") != 1 or src.count("<style>") != 1:
    fail("ожидается ровно один <script> и один <style> — CSP закрепляет их по хешу")

# --- no external resources: the panel must stay self-contained ---------------

for m in re.finditer(r'(?:src|href)="(https?:)?//', src):
    fail("внешний ресурс в панели: " + src[m.start() : m.start() + 60])

if problems:
    print(f"panel.html: {len(problems)} проблем")
    for p in problems:
        print("  •", p)
    sys.exit(1)

print("panel.html: проверки пройдены")
