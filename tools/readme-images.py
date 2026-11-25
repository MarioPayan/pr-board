#!/usr/bin/env python3
"""Generate the README images in docs/images/ from the real demo board.

    python3 tools/readme-images.py

Nothing here is drawn from memory. The script builds pr-board from this
checkout, serves `pr-board demo` (the fictional org acme) on a free port and
reads three sources:

- GET /api/prs, the view the page renders: every band, row, signal and Next;
- `pr-board list` and `pr-board line`, run against that demo's own store,
  with the ANSI colours they print;
- internal/prboard/web/index.html: the colour tokens, the icon paths, the
  signal table (icon, colour, letters, meaning) and the row grid, which it
  checks before laying anything out.

The page images are schematics of the page in light mode, laid out from its
CSS, not screenshots; scripts/shots.mjs takes the real screenshots. As in
AImeter, the SVGs use attributes rather than a <style> block (GitHub serves
README SVGs through an <img>), and every monospace token carries its own
textLength so it lands where it should whatever font the viewer has.

Needs Go and the Python standard library, nothing else.
"""
import json
import os
import pathlib
import re
import signal
import subprocess
import tempfile
import urllib.request
from xml.sax.saxutils import escape

ROOT = pathlib.Path(__file__).resolve().parent.parent
OUT = ROOT / "docs" / "images"
HTML = (ROOT / "internal" / "prboard" / "web" / "index.html").read_text()

SANS = "Inter, -apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif"
DISPLAY = "'Space Grotesk', " + SANS
MONO = "'JetBrains Mono', ui-monospace, SFMono-Regular, Menlo, Consolas, 'DejaVu Sans Mono', monospace"


# ── the page's own rules ─────────────────────────────────────────────────────
def page_rules():
    root = re.search(r":root\{color-scheme:light dark;(.*?)\}", HTML, re.S).group(1)
    long = lambda h: "#" + "".join(c * 2 for c in h[1:]) if len(h) == 4 else h  # noqa: E731
    tok = {k: long(light) for k, light, _ in re.findall(r"--(\w+):light-dark\((#[0-9a-f]+),(#[0-9a-f]+)\)", root)}
    icons = dict(re.findall(r'<symbol id="i-([\w-]+)" viewBox="0 0 24 24">(.*?)</symbol>', HTML))
    block = re.search(r"const K=\{(.*?)\n  \};", HTML, re.S).group(1)
    kinds = {}
    for k, body in re.findall(r"(\w+):\[([^\]]*)\]", block):
        s = re.findall(r'"([^"]*)"', body)
        n = re.search(r"\{n:(\d+)\}", body)
        x = re.search(r'\{x:"([^"]*)"\}', body)
        # [icon, colour, meaning, letters, legend sample, legend note]
        kinds[k] = {"icon": s[0], "cls": s[1], "name": s[2], "letters": s[3] if len(s) > 3 else "",
                    "n": n.group(1) if n else "", "x": x.group(1) if x else ""}
    states = re.findall(r'\["(ready|missing|approve|draft)","(\w+)","([^"]+)"', HTML)
    # the layout below is these rules; refuse to draw if the page changed them
    for rule in (
        r"\.pr\{[^}]*height:42px;display:grid;grid-template-columns:112px 168px minmax\(160px,1fr\) fit-content\(220px\) minmax\(140px,240px\);align-items:center;gap:12px;padding:0 12px;",
        r"\.pr\.merged,\.pr\.rv\{height:34px\}",
        r"\.pr\.sub\{margin-left:28px\}",
        r"\.rows\{margin-left:12px;padding:6px 0 2px 10px;border-left:2px solid var\(--line\);display:grid;gap:6px",
        r"\.ic\{display:inline-flex;flex:0 0 auto;align-items:center;gap:3px;height:22px;font:600 \.78rem var\(--mono\);padding:0 6px;",
        r"\.groups\{display:grid;gap:18px\}",
    ):
        if not re.search(rule, HTML):
            raise SystemExit(f"index.html no longer matches the layout this script draws:\n  {rule}")
    return tok, icons, kinds, states


TOK, ICONS, K, STATES = page_rules()
KCOL = {"k-ok": TOK["green"], "k-bad": TOK["red"], "k-warn": TOK["amber"], "k-info": TOK["muted"]}
BAR = {"ready": TOK["green"], "approve": TOK["amber"], "missing": TOK["red"], "merged": TOK["purple"]}


# ── the binary's own output ──────────────────────────────────────────────────
def capture():
    """Serve the demo, read its view, run list and line against its store."""
    with tempfile.TemporaryDirectory(prefix="pr-board-images-") as tmp:
        tmp = pathlib.Path(tmp)
        exe = tmp / "pr-board"
        subprocess.run(["go", "build", "-o", str(exe), "./cmd/pr-board"], cwd=ROOT, check=True)
        demo_tmp = tmp / "t"
        demo_tmp.mkdir()
        srv = subprocess.Popen([str(exe), "demo", "--port", "0"], env={**os.environ, "TMPDIR": str(demo_tmp)},
                               stdout=subprocess.PIPE, text=True)
        try:
            url = srv.stdout.readline().split()[0]
            with urllib.request.urlopen(url + "api/prs", timeout=10) as r:
                view = json.load(r)
            (home,) = demo_tmp.glob("pr-board-demo-*")
            # `line` starts a real server when no live lock is found: never let it
            if not (home / ".local" / "state" / "pr-board" / "server.json").exists():
                raise SystemExit("the demo server wrote no lock; refusing to run `pr-board line`")
            cfg = home / ".config" / "pr-board" / "config.json"
            cfg.parent.mkdir(parents=True, exist_ok=True)
            cfg.write_text('{"owners":["acme"]}\n')
            env = {"HOME": str(home), "PATH": os.environ["PATH"], "LANG": "C.UTF-8", "LC_ALL": "C.UTF-8",
                   "PR_BOARD_LINK": "", "PR_BOARD_MAX": "8"}
            run = lambda c: subprocess.run([str(exe), c], env=env, capture_output=True, text=True, check=True).stdout  # noqa: E731
            listing, segment = run("list"), run("line")
        finally:
            srv.send_signal(signal.SIGINT)
            srv.wait(timeout=10)
    if not listing.strip() or not segment.strip():
        raise SystemExit("pr-board list or line printed nothing")
    return view, listing, segment


# ── measuring ────────────────────────────────────────────────────────────────
# Helvetica advance widths (per 1000 em); Inter runs a little wider, hence the
# factor in tw(). Only used to place proportional text and cut it with "…".
_W = dict(zip("abcdefghijklmnopqrstuvwxyz", [556, 556, 500, 556, 556, 278, 556, 556, 222, 222, 500, 222, 833,
                                             556, 556, 556, 556, 333, 500, 278, 556, 500, 722, 500, 500, 500]))
_W.update(zip("ABCDEFGHIJKLMNOPQRSTUVWXYZ", [667, 667, 722, 722, 667, 611, 778, 722, 278, 500, 667, 556, 833,
                                             722, 778, 667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611]))
_W.update({c: 556 for c in "0123456789#"})
_W.update({" ": 278, ".": 278, ",": 278, "·": 278, "/": 278, "[": 278, "]": 278, "(": 333, ")": 333,
           "-": 333, "'": 191, ":": 278, "→": 1000, "…": 1000, "+": 584})


def tw(s, size, weight=400):
    return sum(_W.get(c, 600) for c in s) / 1000 * size * (1.1 if weight >= 600 else 1.05)


def mw(s, size):
    return len(s) * size * 0.6


def cut(s, width, measure):
    if measure(s) <= width:
        return s
    while s and measure(s + "…") > width:
        s = s[:-1]
    return s.rstrip() + "…"


def mix(a, b, p):
    """color-mix(in srgb, a p, b)."""
    ca = [int(a[i:i + 2], 16) for i in (1, 3, 5)]
    cb = [int(b[i:i + 2], 16) for i in (1, 3, 5)]
    return "#" + "".join(f"{round(x * p + y * (1 - p)):02x}" for x, y in zip(ca, cb))


# ── drawing ──────────────────────────────────────────────────────────────────
class Svg:
    def __init__(self):
        self.out, self.clips = [], 0

    def add(self, s):
        self.out.append(s)

    def text(self, x, y, s, size, fill, family=SANS, weight=400, anchor="start", length=None, opacity=None):
        extra = f' textLength="{length:.1f}" lengthAdjust="spacing"' if length else ""
        extra += f' opacity="{opacity}"' if opacity else ""
        self.add(f'<text x="{x:.1f}" y="{y:.1f}" font-family="{family}" font-size="{size:.2f}" font-weight="{weight}" '
                 f'text-anchor="{anchor}" fill="{fill}"{extra} xml:space="preserve">{escape(s)}</text>')

    def mono(self, x, y, s, size, fill, weight=500, opacity=None):
        if s.strip():
            self.text(x, y, s, size, fill, MONO, weight, length=mw(s, size), opacity=opacity)

    def rect(self, x, y, w, h, rx=0, fill="none", stroke=None, extra=""):
        st = f' stroke="{stroke}" stroke-width="1"' if stroke else ""
        self.add(f'<rect x="{x:.1f}" y="{y:.1f}" width="{w:.1f}" height="{h:.1f}" rx="{rx}" fill="{fill}"{st}{extra}/>')

    def line(self, x1, y1, x2, y2, stroke, width=1, dash=None):
        d = f' stroke-dasharray="{dash}"' if dash else ""
        self.add(f'<line x1="{x1:.1f}" y1="{y1:.1f}" x2="{x2:.1f}" y2="{y2:.1f}" stroke="{stroke}" stroke-width="{width}"{d}/>')

    def icon(self, name, x, y, size, stroke, width=2.2):
        self.add(f'<g transform="translate({x:.1f},{y:.1f}) scale({size / 24:.4f})" fill="none" stroke="{stroke}" '
                 f'stroke-width="{width}" stroke-linecap="round" stroke-linejoin="round">{ICONS[name]}</g>')

    def boxed(self, x, y, w, h, rx, fill, stroke, left, left_fill, dashed=False):
        """A box with a thicker left border, as CSS draws border-left."""
        self.clips += 1
        cid = f"c{self.clips}"
        self.add(f'<clipPath id="{cid}"><rect x="{x:.1f}" y="{y:.1f}" width="{w:.1f}" height="{h:.1f}" rx="{rx}"/></clipPath>')
        self.rect(x + .5, y + .5, w - 1, h - 1, rx, fill, stroke)
        if dashed:
            for dy in range(0, int(h), 10):
                self.add(f'<rect x="{x:.1f}" y="{y + dy:.1f}" width="{left}" height="5" fill="{left_fill}" clip-path="url(#{cid})"/>')
        else:
            self.add(f'<rect x="{x:.1f}" y="{y:.1f}" width="{left}" height="{h:.1f}" fill="{left_fill}" clip-path="url(#{cid})"/>')

    def svg(self, w, h, label):
        return (f'<svg xmlns="http://www.w3.org/2000/svg" width="{w}" height="{h}" viewBox="0 0 {w} {h}" '
                f'role="img" aria-label="{escape(label)}">' + "".join(self.out) + "</svg>\n")


# ── page pieces ──────────────────────────────────────────────────────────────
LETTER, NUM = 0.72 * 14, 0.78 * 14   # .ic.lt b and .ic sizes, in px


def chip_parts(sig):
    """The chip's children in order, each (kind, value, width)."""
    if sig["k"] == "more":
        return "k-info", [("n", sig["n"], mw(sig["n"], NUM))]
    d = K[sig["k"]]
    parts = []
    if d["letters"]:
        parts.append(("letters", d["letters"], mw(d["letters"], LETTER)))
    parts.append(("icon", d["icon"], 15))
    if sig.get("n"):
        parts.append(("n", str(sig["n"]), mw(str(sig["n"]), NUM)))
    if sig.get("text"):
        t = sig["text"]
        if sig["k"] == "rev" and len(t) > 12:   # .ic .who{max-width:12ch}
            t = t[:11] + "…"
        parts.append(("n", t, mw(t, NUM)))
    return d["cls"], parts


def chip_width(sig):
    _, parts = chip_parts(sig)
    return 2 + 12 + sum(p[2] for p in parts) + 3 * (len(parts) - 1)


def draw_chip(s, x, mid, sig, under):
    cls, parts = chip_parts(sig)
    col = KCOL[cls]
    w = chip_width(sig)
    lettered = sig["k"] != "more" and K[sig["k"]]["letters"]
    s.rect(x + .5, mid - 10.5, w - 1, 21, 6, mix(col, under, .13), col if lettered else None)
    cx = x + 7
    for kind, val, pw in parts:
        if kind == "icon":
            s.icon(val, cx, mid - 7.5, 15, col)
        elif kind == "letters":
            s.mono(cx, mid + 3.6, val, LETTER, col, 800)
        else:
            s.mono(cx, mid + 3.9, val, NUM, col, 600)
        cx += pw + 3
    return w


def fit_signals(card):
    """The signals that show, folding the overflow into "+N" as fitSignals() does."""
    after = card.get("after", "")
    base = (mw(after, 10.64) + 2 + 4) if after else 0
    sigs = list(card.get("signals") or [])
    width = lambda ss: base + sum(chip_width(x) for x in ss) + 4 * max(len(ss) - 1, 0)  # noqa: E731
    if width(sigs) <= 220:
        return sigs, width(sigs)
    for hide in range(1, len(sigs) + 1):
        shown = sigs[:-hide] + [{"k": "more", "n": f"+{hide}"}]
        if width(shown) <= 220:
            return shown, width(shown)
    raise SystemExit(f"signals of {card['key']} do not fit")


def short(repo):
    return repo.split("/")[-1]


def shown_title(card, by):
    if by != "cat" or not card.get("category"):
        return card["title"]
    return re.sub(r"\s{2,}", " ", card["title"].replace("[" + card["category"] + "]", "")).strip() or card["title"]


def draw_row(s, x, y, w, card, by, depth=0):
    """One .pr row; returns its geometry for callouts."""
    if card.get("draft"):
        raise SystemExit(f"{card['key']} is a draft; this script does not draw the DRAFT tag")
    review = by == "review"
    h = 34 if review else 42
    if depth:
        # .pr.sub::before, the elbow from the row above
        ex = x + 28 - 21 + 1
        s.add(f'<path d="M{ex:.1f} {y - 7:.1f}V{y + 14:.1f}q0 6 6 6H{ex + 15:.1f}" fill="none" stroke="{TOK["trunk"]}" stroke-width="2"/>')
        x, w = x + 28, w - 28
    col = BAR[card["state"]]
    under = mix(col, TOK["surface"], .08)
    s.boxed(x, y, w, h, 7, under, mix(col, TOK["line"], .32), 5, col)
    mid = y + h / 2
    c1 = x + 5 + 12
    c2 = c1 + 112 + 12
    c3 = c2 + 168 + 12
    nw = 240
    nx = x + w - 1 - 12 - nw
    sigs, sw = fit_signals(card) if not review else (card.get("signals") or [], None)
    if review:
        sw = sum(chip_width(x_) for x_ in sigs) + 4 * (len(sigs) - 1)
    sx = nx - 12 - sw
    title_w = sx - 12 - c3
    if title_w < 160:
        raise SystemExit("the image is too narrow for the row grid")
    num = f"#{card['n']}"
    s.mono(c1, mid + 4.4, num, 12.6, TOK["fg"], 600)
    s.line(c1, mid + 6.5, c1 + mw(num, 12.6), mid + 6.5, TOK["fg"])
    s.mono(c2, mid + 3.7, short(card["repo"]), 10.64, TOK["muted"], 600)
    s.mono(c2 + mw(short(card["repo"]), 10.64), mid + 3.7, " → " + card["base"], 10.64, TOK["muted"], 600, opacity=.75)
    t = cut(shown_title(card, by), title_w, lambda v: tw(v, 14, 600))
    s.text(c3, mid + 4.9, t, 14, TOK["fg"], weight=600)
    cx = sx
    if card.get("after") and not review:
        s.mono(cx, mid + 3.7, card["after"], 10.64, TOK["muted"], 600)
        cx += mw(card["after"], 10.64) + 2 + 4
    spans = {}
    for sig in sigs:
        cw = draw_chip(s, cx, mid, sig, under)
        spans.setdefault("chips", [cx, cx + cw])[1] = cx + cw
        cx += cw + 4
    if review:
        size, lead, bold = 11.2, "by ", card.get("author", "")
    else:
        size, lead, bold = 11.76, "", "Next:"
    if review:
        s.text(nx, mid + 4, lead, size, TOK["muted"])
        s.text(nx + tw(lead, size), mid + 4, bold, size, TOK["fg"], weight=600)
    else:
        s.text(nx, mid + 4, bold, size, TOK["fg"], weight=600)
        rest = cut(card["next"], nw - tw(bold + " ", size, 600), lambda v: tw(v, size))
        s.text(nx + tw(bold + " ", size, 600), mid + 4, rest, size, TOK["muted"])
    return {"x": x, "y": y, "h": h, "mid": mid, "num": (c1, c1 + mw(num, 12.6)),
            "repo": (c2, c2 + mw(short(card["repo"]) + " → " + card["base"], 10.64)),
            "title": (c3, c3 + min(title_w, tw(t, 14, 600))), "after": (sx, sx + mw(card.get("after", ""), 10.64)),
            "chips": tuple(spans.get("chips", (sx, sx))), "next": (nx, nx + tw("Next: " + card.get("next", ""), size)),
            "elbow": x - 21 + 1 if depth else None}


def counts(rows):
    c = {"ready": 0, "approve": 0, "missing": 0, "draft": 0}
    for r in rows:
        c["draft" if r.get("draft") else r["state"]] += 1
    return c


BAND_H = 37


def draw_band(s, x, y, w, group, review=False):
    """The band button; returns the x spans of its parts."""
    s.boxed(x, y, w, BAND_H, 8, TOK["surface"], TOK["line"], 4, TOK["trunk"])
    mid = y + BAND_H / 2
    cx = x + 4 + 10
    s.text(cx + 7, mid + 4.5, "▾", 14, TOK["muted"], anchor="middle")
    cx += 14 + 10
    name = "To review" if review else (group["key"] or "Untagged")
    s.text(cx, mid + 5.5, name, 16, TOK["fg"], DISPLAY, 600)
    spans = {"name": (cx, cx + tw(name, 16, 600))}
    cx = spans["name"][1] + 10
    repos = " · ".join(short(r) for r in group.get("repos") or [])
    s.text(cx, mid + 4, repos, 10.92, TOK["muted"])
    spans["repos"] = (cx, cx + tw(repos, 10.92))
    right = x + w - 1 - 10
    if review:
        t = f"{len(group['rows'])} to review"
        s.mono(right - mw(t, 10.92), mid + 4, t, 10.92, TOK["muted"], 600)
        return spans
    mg = f"{len(group.get('merged') or [])} merged"
    s.mono(right - mw(mg, 10.92), mid + 4, mg, 10.92, TOK["purple"], 600)
    spans["merged"] = (right - mw(mg, 10.92), right)
    c = counts(group["rows"])
    dots = [(TOK[colour], str(c[key])) for key, colour, _ in STATES]
    widths = [8 + 4 + mw(n, 10.92) for _, n in dots]
    dx = right - mw(mg, 10.92) - 10 - sum(widths) - 10 * (len(dots) - 1)
    spans["dots"] = (dx, right - mw(mg, 10.92) - 10)
    for (colour, n), dw in zip(dots, widths):
        s.add(f'<circle cx="{dx + 4:.1f}" cy="{mid:.1f}" r="4" fill="{colour}"/>')
        s.mono(dx + 12, mid + 4, n, 10.92, TOK["fg"], 600)
        dx += dw + 10
    return spans


def draw_group(s, x, y, w, group, by):
    """A band and its open rows; returns (bottom y, band spans, row geometries)."""
    spans = draw_band(s, x, y, w, group, review=by == "review")
    ry = y + BAND_H + 6
    rows = []
    for r in group["rows"]:
        g = draw_row(s, x + 24, ry, w - 24, r, by, r.get("depth", 0))
        rows.append(g)
        ry += g["h"] + 6
    s.line(x + 13, y + BAND_H, x + 13, ry - 6 + 2, TOK["line"], 2)
    return ry - 6 + 2, spans, rows


# ── image 1: the board ───────────────────────────────────────────────────────
def flex_wrap(items, x, width, gap):
    """Lay items out like flex-wrap: [(w, h, draw, grow)], one line at a time."""
    lines, cur, used = [], [], 0
    for it in items:
        need = it[0] + (gap if cur else 0)
        if cur and used + need > width:
            lines.append(cur)
            cur, used = [], 0
            need = it[0]
        cur.append(it)
        used += need
    lines.append(cur)
    placed = []
    for ln in lines:
        free = width - sum(i[0] for i in ln) - gap * (len(ln) - 1)
        grow = [i for i in ln if i[3]]
        cx = x
        for i in ln:
            placed.append((cx, i))
            cx += i[0] + (free / len(grow) if i in grow else 0) + gap
        placed.append(None)
    return placed


def board_svg(view):
    W, H = 1100, 820
    s = Svg()
    s.rect(0, 0, W, H, 9, TOK["bg"])
    # the browser around it, plainly drawn
    s.add(f'<path d="M0 9a9 9 0 0 1 9-9h{W - 18}a9 9 0 0 1 9 9v31H0z" fill="#e6e8ee"/>')
    for i, c in enumerate(("#d8dbe3", "#d8dbe3", "#d8dbe3")):
        s.add(f'<circle cx="{20 + i * 16}" cy="20" r="5" fill="{c}"/>')
    s.rect(80, 9, 260, 22, 11, TOK["surface"])
    s.text(94, 24.5, "127.0.0.1:7878", 11.5, TOK["muted"])
    s.line(0, 40, W, 40, TOK["line"])
    s.add('<clipPath id="page"><rect x="0" y="41" width="1100" height="779"/></clipPath><g clip-path="url(#page)">')

    X, CW = 20, W - 40
    y = 41 + 20
    # header: title, counters, theme, legend, refresh, clean up
    c = counts([r for g in view["repos"] for r in g["rows"]])
    items = []
    items.append((tw("PR Board", 24, 600), 35, lambda x, m: s.text(x, m + 8, "PR Board", 24, TOK["fg"], DISPLAY, 600), False))
    when = f"{view['open']} open · {view['merged']} merged"
    items.append((tw(when, 11.9), 18, lambda x, m: s.text(x, m + 4, when, 11.9, TOK["muted"]), False))
    pills = []
    for key, colour, name in STATES:
        label = name.lower()
        pw = 2 + 11 + 9 + 6 + mw(str(c[key]), 12.6) + 6 + tw(label, 11.2, 600) + 11
        pills.append((pw, key, colour, label))
    ctr_w = sum(p[0] for p in pills) + 6 * (len(pills) - 1)

    def draw_ctr(x, m):
        for pw, key, colour, label in pills:
            s.rect(x + .5, m - 13.5, pw - 1, 27, 13.5, TOK["surface"], TOK["line"])
            s.add(f'<circle cx="{x + 13 + 4.5:.1f}" cy="{m:.1f}" r="4.5" fill="{TOK[colour]}"/>')
            n = str(c[key])
            s.mono(x + 28, m + 4.4, n, 12.6, TOK["fg"], 700)
            lx = x + 28 + mw(n, 12.6) + 6
            s.text(lx, m + 4, label, 11.2, TOK["fg"], weight=600, length=pw - (lx - x) - 13)
            x += pw + 6
    items.append((ctr_w, 28, draw_ctr, False))
    items.append((0, 0, lambda x, m: None, True))
    theme = [("Auto", True), ("Light", False), ("Dark", False)]
    th_w = sum(20 + tw(t, 11.48, 600) for t, _ in theme) + 2

    def draw_theme(x, m):
        s.rect(x + .5, m - 15.5, th_w - 1, 31, 6, TOK["surface"], TOK["line"])
        cx = x + 1
        for t, on in theme:
            bw = 20 + tw(t, 11.48, 600)
            if on:
                s.rect(cx, m - 15, bw, 30, 0, TOK["fg"])
            s.text(cx + 10, m + 4, t, 11.48, TOK["surface"] if on else TOK["muted"], weight=600, length=bw - 20)
            cx += bw
    items.append((th_w, 32, draw_theme, False))

    def draw_info(x, m):
        s.rect(x + .5, m - 15.5, 31, 31, 6, TOK["surface"], TOK["line"])
        s.icon("info", x + 7, m - 9, 18, TOK["fg"], 2)
    items.append((32, 32, draw_info, False))
    rw = 24 + 2 + tw("Refresh", 11.9, 600)

    def draw_refresh(x, m):
        s.rect(x + .5, m - 15, rw - 1, 30, 6, TOK["surface"], TOK["line"])
        s.text(x + 13, m + 4.2, "Refresh", 11.9, TOK["fg"], weight=600, length=rw - 26)
    items.append((rw, 31, draw_refresh, False))
    badge = str(view["merged"])
    bw = max(20, 12 + mw(badge, 10.92))
    cu_w = 26 + tw("Clean up", 11.9, 600) + 4 + 3 + bw + 3 + 4 + tw("merged", 11.9, 600)

    def draw_cleanup(x, m):
        s.rect(x + .5, m - 15, cu_w - 1, 30, 6, TOK["surface"], TOK["purple"])
        s.text(x + 13, m + 4.2, "Clean up", 11.9, TOK["purple"], weight=600)
        bx = x + 13 + tw("Clean up", 11.9, 600) + 4 + 3
        s.rect(bx, m - 9, bw, 18, 9, TOK["purple"])
        s.mono(bx + (bw - mw(badge, 10.92)) / 2, m + 4, badge, 10.92, TOK["surface"], 700)
        s.text(bx + bw + 3 + 4, m + 4.2, "merged", 11.9, TOK["purple"], weight=600)
    items.append((cu_w, 31, draw_cleanup, False))
    y = place_lines(flex_wrap(items, X, CW, 16), y, 10) + 12

    # filter bar
    bar_items = []
    ph = "Search number, title, branch  ( / )"

    def draw_q(x, m):
        s.rect(x + .5, m - 15.5, 259, 31, 6, TOK["bg"], TOK["line"])
        s.text(x + 10, m + 4, ph, 11.9, TOK["muted"])
    bar_items.append((260, 32, draw_q, False))
    for dd in ("Repo", "State", "Category"):
        dw = 20 + tw(dd, 11.9, 600) + 6 + 6 + 2

        def draw_dd(x, m, dd=dd, dw=dw):
            s.rect(x + .5, m - 15.5, dw - 1, 31, 6, TOK["surface"], TOK["line"])
            s.text(x + 11, m + 4.2, dd, 11.9, TOK["fg"], weight=600)
            vx = x + dw - 11 - 6
            s.add(f'<path d="M{vx:.1f} {m - 3:.1f}l3.5 3.5 3.5-3.5" fill="none" stroke="{TOK["muted"]}" stroke-width="1.5"/>')
        bar_items.append((dw, 32, draw_dd, False))
    gb = [("By category", True), ("By repo", False)]
    gb_w = sum(20 + tw(t, 11.48, 600) for t, _ in gb) + 2

    def draw_gb(x, m):
        s.rect(x + .5, m - 15.5, gb_w - 1, 31, 6, TOK["surface"], TOK["line"])
        cx = x + 1
        for t, on in gb:
            w_ = 20 + tw(t, 11.48, 600)
            if on:
                s.rect(cx, m - 15, w_, 30, 0, TOK["fg"])
            s.text(cx + 10, m + 4, t, 11.48, TOK["surface"] if on else TOK["muted"], weight=600, length=w_ - 20)
            cx += w_
    bar_items.append((gb_w, 32, draw_gb, False))
    tg_w = 12 + 13 + 6 + tw("Show merged", 11.9, 600)

    def draw_tog(x, m):
        s.rect(x + 6.5, m - 6.5, 13, 13, 2.5, TOK["surface"], TOK["muted"])
        s.text(x + 6 + 13 + 6, m + 4.2, "Show merged", 11.9, TOK["fg"], weight=600)
    bar_items.append((tg_w, 32, draw_tog, False))
    bar_items.append((0, 0, lambda x, m: None, True))
    cnt = f"Showing {view['open']} of {view['open']} open"
    bar_items.append((tw(cnt, 11.9), 18, lambda x, m: s.text(x, m + 4, cnt, 11.9, TOK["muted"]), False))
    s.rect(X + .5, y + .5, CW - 1, 47, 8, TOK["surface"], TOK["line"])
    place_lines(flex_wrap(bar_items, X + 9, CW - 18, 8), y + 9, 8)
    y += 48 + 18

    # the bands: to review, then the categories in order, until the window ends
    y, _, _ = draw_group(s, X, y, CW, view["review"], "review")
    for g in view["categories"]:
        if y > H:
            break
        y, _, _ = draw_group(s, X, y + 18, CW, g, "cat")
    s.add("</g>")
    s.rect(.5, .5, W - 1, H - 1, 9, "none", "#c9cdd6")

    return s.svg(W, H, (
        f"PR Board in a browser, light theme, on the fictional demo organization: the header counts "
        f"{c['ready']} ready to merge, {c['missing']} something missing, {c['approve']} ready to approve and "
        f"{c['draft']} drafts, above the filter bar; a To review band lists {view['to_review']} teammates' PRs "
        f"that ask for your review; then one band per category, one row per PR with a coloured left bar, the "
        f"number, repository and branch, title, signal chips and the next step, and a PR that waits on another "
        f"sitting under it."))


def place_lines(placed, y, row_gap):
    """Draw flex_wrap()'s output from y; returns the bottom."""
    line, top = [], y
    for p in placed + [None]:
        if p is None:
            if line:
                h = max(i[1] for _, i in line)
                for x, i in line:
                    i[2](x, top + h / 2)
                top += h + row_gap
                line = []
            continue
        line.append(p)
    return top - row_gap


# ── image 2: one band, annotated ─────────────────────────────────────────────
def anatomy_svg(view):
    W = 1100
    s = Svg()
    LABEL, HEAD, HAIR = "#4a5263", "#7a8191", "#a3aab8"
    s.text(28, 34, "One band of the board, with the PR that waits on another at the bottom", 13, HEAD, MONO)
    s.line(28, 46, W - 28, 46, TOK["line"])

    group = next(g for g in view["categories"] if g["key"] == "Checkout")
    X, BY = 28, 150
    bottom, band, rows = draw_group(s, X, BY, W - 56, group, "cat")
    last = rows[-1]
    if not last["elbow"]:
        raise SystemExit("the Checkout band no longer ends in a chained row")

    def call(x, y1, y2, text, ty, anchor="middle"):
        s.line(x, y1, x, y2, HAIR)
        if text:
            s.text(x, ty, text, 12, LABEL, MONO, anchor=anchor)

    mid = lambda a: (a[0] + a[1]) / 2  # noqa: E731
    # above the band
    call(mid(band["name"]), 104, BY + 6, "category, from [Checkout] in the title", 96, "start")
    call(mid(band["repos"]), 132, BY + 8, "its repositories", 124)
    call(mid(band["dots"]), 132, BY + 8, "open PRs per colour", 124)
    call(mid(band["merged"]), 104, BY + 8, "merged, until Clean up", 96, "end")

    # below the last row; a line never crosses a label above its own
    top = last["y"] + last["h"] + 2
    lv = (top + 26, top + 52, top + 78)
    bx = last["x"] + 2.5
    call(bx, top - 4, lv[2] - 12, "colour: what it still needs", lv[2], "start")
    call(mid(last["num"]), top - 10, lv[0] - 12, "number", lv[0])
    call(mid(last["repo"]), top - 10, lv[1] - 12, "repo → the branch it merges into", lv[1])
    call(mid(last["title"]), top - 10, lv[0] - 12, "title, minus its [Checkout]", lv[0])
    call(mid(last["after"]), top - 10, lv[2] - 12, "waits on #412, the row above", lv[2])
    cx = mid(last["chips"])
    call(cx, top - 10, lv[1] - 12, "signals, always in this order", lv[1], "start")
    call(mid(last["next"]), top - 10, lv[0] - 12, "the next step", lv[0])

    # the strip: colours and every signal, from the page's legend tables
    STRIP = lv[2] + 44
    s.line(28, STRIP - 18, W - 28, STRIP - 18, TOK["line"])
    s.text(28, STRIP, "THE LEFT BAR", 10, HEAD, MONO)
    s.text(300, STRIP, "SIGNALS, IN ROW ORDER", 10, HEAD, MONO)
    bars = [(TOK[colour], name, False) for key, colour, name in STATES if key != "draft"]
    bars += [(TOK["purple"], "Merged", False), (TOK["red"], "Draft: dashed, counted apart", True)]
    for i, (colour, name, dashed) in enumerate(bars):
        yy = STRIP + 26 + i * 26
        if dashed:
            for dy in (0, 7, 14):
                s.rect(28, yy - 13 + dy, 5, 4, 0, colour)
        else:
            s.rect(28, yy - 13, 5, 18, 2, colour)
        s.text(44, yy, name, 12, LABEL)
    kinds = list(K)
    per = (len(kinds) + 1) // 2
    sample = lambda k: {"k": k, "n": K[k]["n"], "text": K[k]["x"]}  # noqa: E731, the legend's own samples
    for col in range(2):
        ks = kinds[col * per:(col + 1) * per]
        col_x = 300 + col * 390
        tx = col_x + max(chip_width(sample(k)) for k in ks) + 12
        for i, k in enumerate(ks):
            yy = STRIP + 26 + i * 26
            draw_chip(s, col_x, yy - 4, sample(k), TOK["bg"])
            s.text(tx, yy, K[k]["name"], 12, LABEL)
            if tx + tw(K[k]["name"], 12) > W - 20:
                raise SystemExit(f"legend text for {k} runs off the image")
    H = STRIP + per * 26 + 14
    frame, s.out = s.out, []
    s.rect(0, 0, W, H, 9, TOK["bg"])
    s.out += frame
    s.rect(.5, .5, W - 1, H - 1, 9, "none", "#c9cdd6")
    return s.svg(W, H, (
        "One band of PR Board, annotated. The band names the category, its repositories, how many open PRs "
        "it has per colour and how many merged ones. Each row has a left bar coloured by what the PR still "
        "needs, its number, the repository and the branch it merges into, the title without its category tag, "
        "the signal chips and the next step; a PR that waits on another sits under it with an elbow and an "
        "after #412 label. Below, the bar colours and every signal chip with its meaning, taken from the "
        "page's legend."))


# ── image 3: the terminal ────────────────────────────────────────────────────
ANSI = {"31": "#e06c6c", "32": "#6cbf6c", "33": "#d7af5f", "36": "#5fb3b3", "90": "#7c818c"}


def ansi_tokens(line, default):
    out, col = [], default
    for part in re.split(r"(\x1b\[[0-9;]*m)", line):
        m = re.fullmatch(r"\x1b\[([0-9;]*)m", part)
        if m:
            col = ANSI.get(m.group(1), default)
        elif part:
            out.append((part, col))
    return out


def terminal_svg(listing, segment):
    BG, CHROME, EDGE, RULE = "#17181c", "#1e2027", "#2a2c33", "#262931"
    BODY, FAINT, PROMPT = "#c3c7cd", "#727783", "#7ba6cd"
    SIZE = 13.5
    ADV = SIZE * 0.6
    PAD = 26
    lines = listing.rstrip("\n").split("\n")
    W = 900
    H = 40 + 30 + 30 + len(lines) * 21 + 70 + 56
    s = Svg()

    def tokens(x0, y, parts, size=SIZE):
        col = 0
        for t, fill in parts:
            if t.strip():
                s.text(x0 + col * size * 0.6, y, t, size, fill, MONO, 500, length=len(t) * size * 0.6)
            col += len(t)
        return col

    s.rect(0, 0, W, H, 8, BG)
    s.add(f'<path d="M0 8a8 8 0 0 1 8-8h{W - 16}a8 8 0 0 1 8 8v30H0z" fill="{CHROME}"/>')
    s.rect(.5, .5, W - 1, H - 1, 8, "none", EDGE)
    s.line(0, 38, W, 38, EDGE)
    s.text(PAD, 25, "~/code/web", 12.5, FAINT, MONO)
    y = 74
    s.text(PAD, y, "!", 14.5, PROMPT, MONO)
    s.text(PAD + 2 * ADV, y, "pr-board list", SIZE, BODY, MONO)
    y += 30
    widest = 0
    for ln in lines:
        widest = max(widest, tokens(PAD + 2 * ADV, y, ansi_tokens(ln, BODY)))
        y += 21
    if PAD + (widest + 2) * ADV > W - PAD:
        raise SystemExit("pr-board list is wider than the terminal image")
    y += 18
    s.text(PAD, y, ">", 14.5, PROMPT, MONO)
    s.rect(PAD + 2 * ADV, y - 11, 8, 15, 0, BODY, extra=' opacity="0.5"')
    bar = H - 30
    s.line(0, bar - 22, W, bar - 22, RULE)
    tokens(PAD, bar, ansi_tokens(segment.strip("\n"), BODY), 15)
    plain = lambda t: re.sub(r"\x1b\[[0-9;]*m", "", t).strip()  # noqa: E731
    return s.svg(W, H, (
        f"A terminal running pr-board list on the fictional demo organization: the header "
        f"{plain(lines[0])!r}, then one row per PR with two lights, CI then review, the repository, the number "
        f"in its state's colour and the title, worst first, ending in {plain(lines[-1])!r}. Along the bottom, "
        f"the statusline segment {plain(segment)!r}: ready to merge, something missing, ready to approve, to "
        f"review and drafts."))


def main():
    view, listing, segment = capture()
    OUT.mkdir(parents=True, exist_ok=True)
    for name, body in (("board.svg", board_svg(view)), ("anatomy.svg", anatomy_svg(view)),
                       ("terminal.svg", terminal_svg(listing, segment))):
        (OUT / name).write_text(body)
        print("wrote", OUT / name)


if __name__ == "__main__":
    main()
