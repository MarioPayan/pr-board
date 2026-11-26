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
    short = dict(re.findall(r'(ready|missing|approve|draft):"(\w+)"', re.search(r"const SHORT=\{(.*?)\};", HTML).group(1)))
    # the next-step kinds: [icon, legend sample, meaning, your move]
    block = re.search(r"const NX=\{(.*?)\n  \};", HTML, re.S).group(1)
    nexts = {k.strip('"'): {"icon": i, "sample": x, "name": m, "mine": f == "1"}
             for k, i, x, m, f in re.findall(r'("?[\w-]+"?):\["(\w+)","([^"]*)","([^"]*)",(\d)\]', block)}
    # the layout below is these rules; refuse to draw if the page changed them
    for rule in (
        r"\.pb-top\{position:sticky;top:0;[^}]*grid-template-columns:auto minmax\(200px,1fr\) auto auto auto;[^}]*gap:12px;margin:-20px -20px 14px;padding:10px 20px;",
        r"\.pr\{[^}]*display:grid;grid-template-columns:72px minmax\(0,1fr\) auto;grid-template-areas:\"num ti nx\" \"num meta nx\";align-items:center;column-gap:12px;row-gap:3px;padding:8px 12px;",
        r"\.ttl\{grid-area:ti;font-weight:600;line-height:20px;[^}]*-webkit-line-clamp:2;",
        r"\.meta\{grid-area:meta;display:flex;align-items:center;gap:10px;min-width:0;height:22px\}",
        r"\.rp\{font:600 \.76rem var\(--mono\);color:var\(--muted\);flex:0 0 auto;max-width:42%;",
        r"\.next\{grid-area:nx;justify-self:end;display:flex;max-width:260px;",
        r"\.nx\{display:inline-flex;align-items:center;gap:6px;max-width:100%;height:26px;padding:0 11px 0 8px;border-radius:999px;font:600 \.8rem var\(--body\);",
        r"\.pr\.wait>\*\{opacity:\.5;",
        r"\.pr\.sub\{margin-left:28px\}",
        r"\.rows\{margin:8px 0 0 6px;padding:2px 0 2px 12px;border-left:2px solid var\(--line\);display:grid;gap:6px",
        r"\.band\{display:flex;align-items:center;gap:10px;[^}]*background:none;border:0;[^}]*padding:2px 2px 2px 0;",
        r"\.mix\{display:flex;gap:2px;width:140px;height:6px;",
        r"\.ic\{display:inline-flex;flex:0 0 auto;align-items:center;gap:3px;height:22px;font:600 \.78rem var\(--mono\);padding:0 6px;",
        r"\.groups\{display:grid;gap:18px\}",
    ):
        if not re.search(rule, HTML):
            raise SystemExit(f"index.html no longer matches the layout this script draws:\n  {rule}")
    if not nexts or len(short) != 4:
        raise SystemExit("index.html: the NX or SHORT table no longer parses")
    return tok, icons, kinds, states, short, nexts


TOK, ICONS, K, STATES, SHORT, NX = page_rules()
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


def fit_signals(card, avail):
    """The signals that show in avail px, folding the overflow into "+N" as fitSignals() does."""
    after = card.get("after", "")
    base = (mw(after, 10.64) + 2 + 4) if after else 0
    sigs = list(card.get("signals") or [])
    width = lambda ss: base + sum(chip_width(x) for x in ss) + 4 * max(len(ss) - 1, 0)  # noqa: E731
    if width(sigs) <= avail:
        return sigs, width(sigs)
    for hide in range(1, len(sigs) + 1):
        shown = sigs[:-hide] + [{"k": "more", "n": f"+{hide}"}]
        if width(shown) <= avail:
            return shown, width(shown)
    raise SystemExit(f"signals of {card['key']} do not fit")


def short(repo):
    return repo.split("/")[-1]


def shown_title(card, by):
    if by != "cat" or not card.get("category"):
        return card["title"]
    return re.sub(r"\s{2,}", " ", card["title"].replace("[" + card["category"] + "]", "")).strip() or card["title"]


def wrap2(text, width, measure):
    """The title on up to two lines, as -webkit-line-clamp:2 cuts it."""
    if measure(text) <= width:
        return [text]
    words, first = text.split(" "), ""
    while words and measure((first + " " + words[0]).strip()) <= width:
        first = (first + " " + words.pop(0)).strip()
    if not first:  # one long word: break it, as overflow-wrap:anywhere does
        first = cut(text, width, measure).rstrip("…")
        words = [text[len(first):]]
    return [first, cut(" ".join(words), width, measure)]


NEXT_SIZE = 0.8 * 14   # .nx font size, in px


def cap(t):
    return t[:1].upper() + t[1:]


def next_width(text):
    """.nx: border, padding 8, icon 15, gap 6, text, padding 11, border; at most .next's 260px."""
    return min(260, 1 + 8 + 15 + 6 + tw(cap(text), NEXT_SIZE, 600) + 11 + 1)


def draw_next(s, right, mid, kind, text, mine, under):
    """The next-step chip, right-aligned at right; returns its x span."""
    w = next_width(text)
    x = right - w
    nx = NX.get(kind)
    if mine:
        fill, edge, ink = TOK["fg"], TOK["fg"], TOK["surface"]
    else:
        fill, edge, ink = under, mix(TOK["muted"], under, .6), TOK["muted"]
    s.rect(x + .5, mid - 12.5, w - 1, 25, 12.5, fill, edge)
    cx = x + 1 + 8
    if nx:
        s.icon(nx["icon"], cx, mid - 7.5, 15, ink)
        cx += 15 + 6
    s.text(cx, mid + 4, cut(cap(text), x + w - 12 - cx + .5, lambda v: tw(v, NEXT_SIZE, 600)), NEXT_SIZE, ink, weight=600)
    return (x, x + w)


def row_height(lines):
    """.pr: border 1, padding 8, the title's lines of 20px, row gap 3, meta 22, padding 8, border 1."""
    return 1 + 8 + 20 * lines + 3 + 22 + 8 + 1


def draw_row(s, x, y, w, card, by, depth=0):
    """One .pr row; returns its geometry for callouts."""
    if card.get("draft"):
        raise SystemExit(f"{card['key']} is a draft; this script does not draw the DRAFT tag")
    review = by == "review"
    if depth:
        x, w = x + 28, w - 28
    col = BAR[card["state"]]
    under = mix(col, TOK["surface"], .08)
    left = x + 5 + 12                              # the number column, 72px
    right = x + w - 1 - 12
    nw = next_width(card["next"]) if card.get("next") else 0
    c2 = left + 72 + 12                            # title and meta
    c2_end = right - (nw + 12 if nw else 0)
    if c2_end - c2 < 300:
        raise SystemExit("the image is too narrow for the row grid")
    lines = wrap2(shown_title(card, by), c2_end - c2, lambda v: tw(v, 14, 600))
    h = row_height(len(lines))
    if depth:
        # .pr.sub::before, the elbow from the row above
        ex = x - 21 + 1
        s.add(f'<path d="M{ex:.1f} {y - 7:.1f}V{y + 14:.1f}q0 6 6 6H{ex + 15:.1f}" fill="none" stroke="{TOK["trunk"]}" stroke-width="2"/>')
    s.boxed(x, y, w, h, 7, under, mix(col, TOK["line"], .32), 5, col)
    wait = not review and card.get("next") and not card.get("your_move")
    if wait:   # .pr.wait>*: the content fades, the card does not
        s.add('<g opacity="0.5">')
    top = y + 1 + 8
    num = f"#{card['n']}"
    s.mono(left, top + 14.6, num, 12.6, TOK["fg"], 600)
    s.line(left, top + 16.7, left + mw(num, 12.6), top + 16.7, TOK["fg"])
    for i, ln in enumerate(lines):
        s.text(c2, top + 14.8 + 20 * i, ln, 14, TOK["fg"], weight=600)
    title = (c2, c2 + max(tw(ln, 14, 600) for ln in lines))
    meta_mid = top + 20 * len(lines) + 3 + 11
    rp_name, rp_base = short(card["repo"]), " → " + card["base"]
    rp_w = mw(rp_name + rp_base, 10.64)
    if rp_w > (c2_end - c2) * .42:
        raise SystemExit(f"{card['key']}: the branch would be cut; this script does not cut it")
    s.mono(c2, meta_mid + 3.7, rp_name, 10.64, TOK["muted"], 600)
    s.mono(c2 + mw(rp_name, 10.64), meta_mid + 3.7, rp_base, 10.64, TOK["muted"], 600, opacity=.75)
    sx = c2 + rp_w + 10
    by_w = (10 + tw("by " + card.get("author", ""), 11.2, 600)) if review else 0
    sigs, sw = fit_signals(card, c2_end - sx - by_w)
    cx = sx
    if card.get("after") and not review:
        s.mono(cx, meta_mid + 3.7, card["after"], 10.64, TOK["muted"], 600)
        cx += mw(card["after"], 10.64) + 2 + 4
    chips = [cx, cx]
    for sig in sigs:
        cw = draw_chip(s, cx, meta_mid, sig, under)
        chips[1] = cx + cw
        cx += cw + 4
    if review:
        bx = sx + sw + 10
        s.text(bx, meta_mid + 4, "by ", 11.2, TOK["muted"])
        s.text(bx + tw("by ", 11.2), meta_mid + 4, card.get("author", ""), 11.2, TOK["fg"], weight=600)
    nspan = draw_next(s, right, y + h / 2, card.get("next_kind", ""), card["next"], card.get("your_move"), under) if nw else (right, right)
    if wait:
        s.add("</g>")
    return {"x": x, "y": y, "h": h, "mid": y + h / 2, "meta": meta_mid, "top": top + 10,
            "num": (left, left + mw(num, 12.6)), "repo": (c2, c2 + rp_w), "title": title,
            "after": (sx, sx + mw(card.get("after", ""), 10.64)), "chips": tuple(chips), "next": nspan,
            "elbow": x - 21 + 1 if depth else None}


def counts(rows):
    c = {"ready": 0, "approve": 0, "missing": 0, "draft": 0}
    for r in rows:
        c["draft" if r.get("draft") else r["state"]] += 1
    return c


BAND_H = 26   # .band: padding 2, the 1.1rem name at line-height 1.45


def draw_mix(s, x, mid, width, c):
    """.mix: one segment per counter, in counter order, 2px apart."""
    parts = [(TOK[colour], c[key]) for key, colour, _ in STATES if c[key]]
    total = sum(n for _, n in parts)
    s.clips += 1
    cid = f"c{s.clips}"
    s.add(f'<clipPath id="{cid}"><rect x="{x:.1f}" y="{mid - 3:.1f}" width="{width}" height="6" rx="3"/></clipPath><g clip-path="url(#{cid})">')
    free = width - 2 * (len(parts) - 1)
    for colour, n in parts:
        sw = free * n / total
        s.rect(x, mid - 3, sw, 6, 0, colour)
        x += sw + 2
    s.add("</g>")


def draw_band(s, x, y, w, group, review=False):
    """The plain group heading; returns the x spans of its parts."""
    mid = y + BAND_H / 2
    cx = x
    s.text(cx + 7, mid + 4.5, "▾", 14, TOK["muted"], anchor="middle")
    cx += 14 + 10
    name = "To review" if review else (group["key"] or "Untagged")
    s.text(cx, mid + 5.5, name, 15.4, TOK["fg"], DISPLAY, 600)
    spans = {"name": (cx, cx + tw(name, 15.4, 600))}
    cx = spans["name"][1] + 10
    repos = " · ".join(short(r) for r in group.get("repos") or [])
    s.text(cx, mid + 4, repos, 10.92, TOK["muted"])
    spans["repos"] = (cx, cx + tw(repos, 10.92))
    right = x + w - 2
    if review:
        tail = [(f"{len(group['rows'])} to review", TOK["muted"], "count")]
    else:
        tail = [(f"{len(group['rows'])} open", TOK["muted"], "total"),
                (f"{len(group.get('merged') or [])} merged", TOK["purple"], "merged")]
    tx = right - sum(mw(t, 10.92) for t, _, _ in tail) - 10 * (len(tail) - 1)
    spans["mix"] = (tx - 10 - 140, tx - 10)
    draw_mix(s, spans["mix"][0], mid, 140, counts(group["rows"]))
    for t, colour, key in tail:
        s.mono(tx, mid + 4, t, 10.92, colour, 600)
        spans[key] = (tx, tx + mw(t, 10.92))
        tx += mw(t, 10.92) + 10
    return spans


def draw_group(s, x, y, w, group, by):
    """A heading and its open rows; returns (bottom y, heading spans, row geometries)."""
    spans = draw_band(s, x, y, w, group, review=by == "review")
    ry = y + BAND_H + 8 + 2
    rows = []
    for r in group["rows"]:
        g = draw_row(s, x + 20, ry, w - 20, r, by, r.get("depth", 0))
        rows.append(g)
        ry += g["h"] + 6
    s.line(x + 7, y + BAND_H + 8, x + 7, ry - 6 + 2, TOK["line"], 2)
    return ry - 6 + 2, spans, rows


# ── image 1: the board ───────────────────────────────────────────────────────
def board_svg(view):
    W, H = 1480, 1000   # the page at 1440px wide: the summary line shows, "merged" on Clean up does not
    s = Svg()
    s.rect(0, 0, W, H, 9, TOK["bg"])
    # the browser around it, plainly drawn
    s.add(f'<path d="M0 9a9 9 0 0 1 9-9h{W - 18}a9 9 0 0 1 9 9v31H0z" fill="#e6e8ee"/>')
    for i, c in enumerate(("#d8dbe3", "#d8dbe3", "#d8dbe3")):
        s.add(f'<circle cx="{20 + i * 16}" cy="20" r="5" fill="{c}"/>')
    s.rect(80, 9, 260, 22, 11, TOK["surface"])
    s.text(94, 24.5, "127.0.0.1:7878", 11.5, TOK["muted"])
    s.line(0, 40, W, 40, TOK["line"])
    s.add(f'<clipPath id="page"><rect x="0" y="41" width="{W}" height="{H - 41}"/></clipPath><g clip-path="url(#page)">')

    X, CW = 20, W - 40
    # the command bar: a 53px strip across the page, its items 32px high
    top, m = 41, 41 + 10 + 16
    s.rect(0, top, W, 52, 0, TOK["bg"])
    s.line(0, top + 52.5, W, top + 52.5, TOK["line"])
    c = counts([r for g in view["repos"] for r in g["rows"]])
    when = f"{view['open']} open · updated 12:48 PM"
    brand_w = tw("PR Board", 16.1, 600) + 10 + tw(when, 11.48)
    segs = [(key, colour, str(c[key]), SHORT[key]) for key, colour, _ in STATES]
    seg_w = [10 + 8 + 6 + mw(n, 12.32) + 6 + tw(lbl, 11.2, 500) + 10 for _, _, n, lbl in segs]
    ctr_w = sum(seg_w) + (len(segs) - 1) + 2
    theme = [("Auto", True), ("Light", False), ("Dark", False)]
    th_w = sum(20 + tw(t, 11.48, 600) for t, _ in theme) + 2
    rf_w = 1 + 12 + 15 + 6 + tw("Refresh", 11.9, 600) + 12 + 1
    badge = str(view["merged"])
    bw = max(20, 12 + mw(badge, 10.92))
    cu_w = 1 + 12 + tw("Clean up", 11.9, 600) + 6 + bw + 12 + 1   # "merged" hides under 1500px
    tools_w = 32 + 8 + rf_w + 8 + cu_w
    q_w = CW - brand_w - ctr_w - th_w - tools_w - 4 * 12
    if q_w < 200:
        raise SystemExit("the command bar does not fit the image")
    x = X
    s.text(x, m + 5.5, "PR Board", 16.1, TOK["fg"], DISPLAY, 600)
    s.text(x + tw("PR Board", 16.1, 600) + 10, m + 4, when, 11.48, TOK["muted"])
    x += brand_w + 12
    s.rect(x + .5, m - 15.5, q_w - 1, 31, 6, TOK["surface"], TOK["line"])
    s.text(x + 10, m + 4, "Search number, title, branch", 11.9, TOK["muted"])
    s.rect(x + q_w - 8 - 17 + .5, m - 7.5, 16, 15, 4, TOK["bg"], TOK["line"])
    s.mono(x + q_w - 8 - 17 + 5, m + 3.5, "/", 9.8, TOK["muted"], 600)
    x += q_w + 12
    s.rect(x + .5, m - 15.5, ctr_w - 1, 31, 8, TOK["surface"], TOK["line"])
    sx = x + 1
    for (key, colour, n, lbl), sw_ in zip(segs, seg_w):
        if sx > x + 1:
            s.line(sx - .5, m - 15, sx - .5, m + 15, TOK["line"])
        s.add(f'<circle cx="{sx + 10 + 4:.1f}" cy="{m:.1f}" r="4" fill="{TOK[colour]}"/>')
        s.mono(sx + 10 + 8 + 6, m + 4.4, n, 12.32, TOK["fg"], 700)
        s.text(sx + 10 + 8 + 6 + mw(n, 12.32) + 6, m + 4, lbl, 11.2, TOK["muted"], weight=500)
        sx += sw_ + 1
    x += ctr_w + 12
    s.rect(x + .5, m - 15.5, th_w - 1, 31, 6, TOK["surface"], TOK["line"])
    cx = x + 1
    for t, on in theme:
        bw_ = 20 + tw(t, 11.48, 600)
        if on:
            s.rect(cx, m - 15, bw_, 30, 0, TOK["fg"])
        s.text(cx + 10, m + 4, t, 11.48, TOK["surface"] if on else TOK["muted"], weight=600, length=bw_ - 20)
        cx += bw_
    x += th_w + 12
    s.rect(x + .5, m - 15.5, 31, 31, 6, TOK["surface"], TOK["line"])
    s.icon("info", x + 7, m - 9, 18, TOK["fg"], 2)
    x += 32 + 8
    s.rect(x + .5, m - 15.5, rf_w - 1, 31, 6, TOK["surface"], TOK["line"])
    s.icon("refresh", x + 13, m - 7.5, 15, TOK["fg"])
    s.text(x + 13 + 15 + 6, m + 4.2, "Refresh", 11.9, TOK["fg"], weight=600)
    x += rf_w + 8
    s.rect(x + .5, m - 15.5, cu_w - 1, 31, 6, TOK["surface"], TOK["purple"])
    s.text(x + 13, m + 4.2, "Clean up", 11.9, TOK["purple"], weight=600)
    bx = x + 13 + tw("Clean up", 11.9, 600) + 6
    s.rect(bx, m - 9, bw, 18, 9, TOK["purple"])
    s.mono(bx + (bw - mw(badge, 10.92)) / 2, m + 4, badge, 10.92, TOK["surface"], 700)
    y = top + 53 + 14

    # filter bar
    bar_items = []
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

    # the groups: to review, then the categories in order, until the window ends
    y, _, _ = draw_group(s, X, y, CW, view["review"], "review")
    for g in view["categories"]:
        if y > H:
            break
        y, _, _ = draw_group(s, X, y + 18, CW, g, "cat")
    s.add("</g>")
    s.rect(.5, .5, W - 1, H - 1, 9, "none", "#c9cdd6")

    return s.svg(W, H, (
        f"PR Board in a browser, light theme, on the fictional demo organization: a command bar with the "
        f"search and the counters {c['ready']} ready, {c['missing']} missing, {c['approve']} approve and "
        f"{c['draft']} drafts, above the filter bar; a To review group lists {view['to_review']} teammates' PRs "
        f"that ask for your review; then one group per category, its heading with a bar of its status mix, and "
        f"one row per PR with a coloured left bar, the number, the title, the repository, branch and signal "
        f"chips under it, and the next step as a chip on the right: solid when it is your move, outlined on a "
        f"faded row when you wait. A PR that waits on another sits under it."))


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


# ── image 2: one group, annotated ────────────────────────────────────────────
def anatomy_svg(view):
    W = 1100
    s = Svg()
    LABEL, HEAD, HAIR = "#4a5263", "#7a8191", "#a3aab8"
    s.text(28, 34, "One group of the board, with the PR that waits on another at the bottom", 13, HEAD, MONO)
    s.line(28, 46, W - 28, 46, TOK["line"])

    group = next(g for g in view["categories"] if g["key"] == "Checkout")
    X, BY = 28, 150
    bottom, band, rows = draw_group(s, X, BY, W - 56, group, "cat")
    last = rows[-1]
    if not last["elbow"]:
        raise SystemExit("the Checkout group no longer ends in a chained row")
    mine = next((r for r, c in zip(rows, group["rows"]) if c.get("your_move")), None)
    if not mine:
        raise SystemExit("the Checkout group has no row that is your move")

    def call(x, y1, y2, text, ty, anchor="middle"):
        s.line(x, y1, x, y2, HAIR)
        if text:
            s.text(x, ty, text, 12, LABEL, MONO, anchor=anchor)

    mid = lambda a: (a[0] + a[1]) / 2  # noqa: E731
    # above the heading
    call(mid(band["name"]), 104, BY + 4, "category, from [Checkout] in the title", 96, "start")
    call(mid(band["repos"]), 132, BY + 6, "its repositories", 124)
    call(mid(band["mix"]), 132, BY + 9, "open PRs per colour", 124)
    call(mid(band["merged"]), 104, BY + 6, "open, and merged until Clean up", 96, "end")

    # left of the first chip that is your move, in the row's empty middle
    lx = mine["next"][0] - 6
    if lx - 30 - mw("solid: your move", 12) < max(mine["chips"][1], mine["title"][1]) + 20:
        raise SystemExit("no room for the your-move callout")
    s.line(lx - 24, mine["mid"], lx, mine["mid"], HAIR)
    s.text(lx - 30, mine["mid"] + 4, "solid: your move", 12, LABEL, MONO, anchor="end")

    # below the last row. Labels start at their line and run right; placed
    # right to left, each takes the first level where it overlaps no label,
    # so no line ever crosses a label above its own.
    top = last["y"] + last["h"] + 2
    meta_end = max(last["chips"][1], last["after"][1])
    title_x = max(mid(last["title"]), meta_end + 14)
    if title_x > last["title"][1] - 4:
        raise SystemExit("the title of the last row has no clear spot for its callout")
    nxt = "next step: outlined and faded, you wait"
    callouts = [  # (x, from y, label, anchor)
        (mid(last["next"]), last["mid"] + 13, nxt, "end"),
        (title_x, last["top"] + 8, "title first, minus its [Checkout]", "start"),
        (mid(last["chips"]), last["meta"] + 12, "signals, always in this order", "start"),
        (mid(last["after"]), last["meta"] + 8, "waits on #412, the row above", "start"),
        (mid(last["repo"]), last["meta"] + 8, "repo → the branch it merges into", "start"),
        (mid(last["num"]), last["top"] + 8, "number", "start"),
        (last["x"] + 2.5, top - 4, "colour: what it still needs", "start"),
    ]
    taken = []   # (level, x0, x1, line x)

    def fits(lvl, x0, x1, x):
        for L, a, b, lx in taken:
            if L == lvl and x0 < b + 16 and a < x1 + 16:
                return False   # overlaps a label on its level
            if L < lvl and a - 6 <= x <= b + 6:
                return False   # its line would cross a label above
            if L > lvl and x0 - 6 <= lx <= x1 + 6:
                return False   # its label would sit on a deeper line
        return True
    for x, y1, text, anchor in callouts:
        w = mw(text, 12)
        x0, x1 = (x - w, x) if anchor == "end" else (x, x + w)
        lvl = 0
        while not fits(lvl, x0, x1, x):
            lvl += 1
            if lvl > 8:
                raise SystemExit(f"no level for the callout {text!r}")
        taken.append((lvl, x0, x1, x))
        ly = top + 26 * (lvl + 1)
        call(x, y1, ly - 12, text, ly, anchor)
    lv = (top + 26 * (max(t[0] for t in taken) + 1),) * 3

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

    # the next steps, from the legend's own table: yours first, then the waits
    NEXT = STRIP + per * 26 + 40
    s.line(28, NEXT - 18, W - 28, NEXT - 18, TOK["line"])
    s.text(28, NEXT, "NEXT STEP: SOLID WHEN IT IS YOUR MOVE, OUTLINED WHEN YOU WAIT", 10, HEAD, MONO)
    ny, nx_ = NEXT + 24, 28
    for kind, d in sorted(NX.items(), key=lambda kv: not kv[1]["mine"]):
        w = next_width(d["sample"])
        if nx_ + w > W - 28:
            ny, nx_ = ny + 34, 28
        draw_next(s, nx_ + w, ny, kind, d["sample"], d["mine"], TOK["bg"])
        nx_ += w + 8
    H = ny + 30
    frame, s.out = s.out, []
    s.rect(0, 0, W, H, 9, TOK["bg"])
    s.out += frame
    s.rect(.5, .5, W - 1, H - 1, 9, "none", "#c9cdd6")
    return s.svg(W, H, (
        "One group of PR Board, annotated. The heading names the category and its repositories, then a bar of "
        "its open PRs per colour, how many are open and how many merged. Each row has a left bar coloured by "
        "what the PR still needs and its number; the title comes first, without its category tag, with the "
        "repository, the branch it merges into and the signal chips under it; the next step is a chip on the "
        "right, solid when it is your move and outlined, on a faded row, when you wait. A PR that waits on "
        "another sits under it with an elbow and an after #412 label. Below, the bar colours, every signal "
        "chip with its meaning and every next-step chip, taken from the page's legend."))


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
