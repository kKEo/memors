package studio

// pageCSS styles the studio pages; diagram.CSS is appended for the inline
// pictures. Served from /static/studio.css (the CSP allows no inline style).
const pageCSS = `
:root { --fg:#1b1b1b; --muted:#666; --line:#ddd; --bg:#fff; --accent:#1a5fb4; --warn:#b4541a; --ok:#1a7a3a; --soft:#f6f6f4; }
* { box-sizing:border-box; }
body { font:15px/1.45 system-ui, -apple-system, "Segoe UI", sans-serif; color:var(--fg); background:var(--bg); margin:0; }
header { border-bottom:1px solid var(--line); padding:.6rem 1rem; display:flex; gap:1rem; align-items:center; flex-wrap:wrap; }
header a { color:var(--fg); text-decoration:none; } header a:hover { text-decoration:underline; }
header .brand { font-weight:700; }
main { max-width:1400px; margin:0 auto; padding:1rem; }
h1 { font-size:1.4rem; margin:.4rem 0 .6rem; } h2 { font-size:1.1rem; margin:1.4rem 0 .5rem; } h3 { font-size:1rem; margin:1rem 0 .4rem; }
table { border-collapse:collapse; width:100%; font-size:.92em; } th, td { text-align:left; padding:.3rem .5rem; border-bottom:1px solid var(--line); vertical-align:top; }
th { color:var(--muted); font-weight:600; } td.num, th.num { text-align:right; font-variant-numeric:tabular-nums; }
code { background:var(--soft); padding:0 .25em; border-radius:3px; font-size:.92em; }
.muted { color:var(--muted); } .warn { color:var(--warn); } .ok { color:var(--ok); } .small { font-size:.85em; }
.badge { display:inline-block; padding:0 .45em; border:1px solid var(--line); border-radius:999px; font-size:.8em; color:var(--muted); margin:0 .1em .2em 0; }
.badge.open { border-color:#b8860b; color:#7a5a0a; background:#fdf3d7; } .badge.decided { border-color:var(--ok); color:var(--ok); }
.badge.na { opacity:.45; }
button { font:inherit; padding:.25rem .7rem; border:1px solid var(--accent); background:var(--accent); color:#fff; border-radius:4px; cursor:pointer; }
form.inline { display:inline; }
.tools { display:flex; gap:.6rem; align-items:center; flex-wrap:wrap; }
.picture { overflow:auto; border:1px solid var(--line); border-radius:6px; background:#faf8f4; margin:.6rem 0 1rem; }
.picture svg { display:block; }
.issues { border:1px solid var(--warn); border-radius:6px; padding:.4rem .9rem; background:#fdf1ee; } .issues ul { margin:.3rem 0; }
pre.source { background:var(--soft); padding:.8rem; overflow:auto; border-radius:4px; font-size:.85em; line-height:1.45; counter-reset:none; }
pre.source .ln { display:inline-block; width:3.2em; color:var(--muted); text-align:right; margin-right:1em; user-select:none; }
pre.source .ln:target, pre.source .ln:target + span { background:#fdf3d7; }
tr.fail td { background:#fdf1ee; } tr.pass td.state { color:var(--ok); }
.cols { display:grid; grid-template-columns:repeat(auto-fit, minmax(380px,1fr)); gap:1.2rem; }
.cell-empty { color:var(--warn); }
footer { color:var(--muted); font-size:.85em; padding:1rem; text-align:center; border-top:1px solid var(--line); margin-top:2rem; }
`
