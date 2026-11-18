# ux/AGENTS.md

The review page: one static HTML shell (`html/index.html`), its stylesheet
(`css/`) and its JavaScript modules (`js/`). Every backend serves these files
as they are, and only these: `/static` serves nothing else under `ux/` (its
tests and these notes). Read the top-level `AGENTS.md` first.

- Vanilla JavaScript modules over the HTTP API in `standards/API.md`. No
  template engine, no jQuery, no framework, no build step.
- The page talks to the server only through the routes in
  `standards/API.md`, by relative URL (`api/...`, `static/...`). A change
  that needs a new route or field changes `standards/API.md` in the same
  commit, and every backend with it.
- Nothing here names a backend: no language, paths or behavior of one.
- Look and theme follow Test2-Harness-UI
  (`~/projects/Test2/Test2-Harness-UI/share/`); read its assets before
  writing new ones.
- highlight.js is vendored, minified, with its license, in `js/highlight/`.
- Pure functions live in `js/logic.js` and are unit tested in
  `t/logic.test.mjs`: `node --test ux/t/` from the top of the repository.
  `package.json` only tells node that `js/` holds ES modules; older node
  reads them as CommonJS without it.
  Interactions are checked by `conformance/page-drive.mjs`.
