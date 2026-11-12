#!/usr/bin/env node
// Drive the page in headless Chrome against a running server of any backend,
// serving the conformance fixture review, and check its interactions. Prints
// TAP lines; exits non-zero on failure. With --topics, it checks the fixture's
// squashed review split into topics instead. The fixtures are described in
// conformance/README.md, "Fixtures".
// Usage: node conformance/page-drive.mjs [--topics] URL [screenshot-dir]
// URL is the one the server printed. CHROME names the browser binary
// (default google-chrome-stable). REVIEW_HELPER_E2E_TIMEOUT is the seconds
// the whole run may take before it fails (default 180).
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { identityLine, walkSteps, FLAG_COLORS, speechSentences } from '../ux/js/logic.js';

const topics = process.argv[2] === '--topics';
const [url, shots = null] = process.argv.slice(topics ? 3 : 2);
if (!url) {
    console.error('Usage: node conformance/page-drive.mjs [--topics] URL [screenshot-dir]');
    process.exit(2);
}
const base = new URL('/', url).href;
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'page-drive-'));
const sleep = ms => new Promise(r => setTimeout(r, ms));

let failed = 0;
let count = 0;
function check(name, ok, info) {
    count++;
    if (!ok) failed++;
    console.log(`${ok ? 'ok' : 'not ok'} ${count} - ${name}${ok || info === undefined ? '' : ' # ' + JSON.stringify(info)}`);
}

// Port 0: Chrome picks a free port and writes it to DevToolsActivePort.
const chrome = spawn(process.env.CHROME || 'google-chrome-stable', [
    '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    '--remote-debugging-port=0', `--user-data-dir=${tmp}`, '--window-size=1280,900', 'about:blank',
], { stdio: 'ignore' });
const chromeExit = new Promise(r => chrome.once('exit', r));

async function finish(code) {
    chrome.kill();
    await Promise.race([chromeExit, sleep(3000)]);
    fs.rmSync(tmp, { recursive: true, force: true });
    process.exit(code);
}

const limit = Number(process.env.REVIEW_HELPER_E2E_TIMEOUT) || 180;
setTimeout(() => {
    console.log(`not ok ${++count} - driver finished in time # ${limit}s`);
    console.log(`1..${count}`);
    finish(1);
}, limit * 1000).unref();
// A line every ten seconds, so whoever reads the output sees the run is
// alive even while one step hangs; killed, it still cleans up.
setInterval(() => console.log('# alive'), 10000).unref();
for (const signal of ['SIGTERM', 'SIGINT', 'SIGHUP']) process.once(signal, () => finish(1));
// Whoever reads the output gone, the next write fails; clean up then too.
process.stdout.on('error', () => finish(1));

let ws;
let seq = 0;
const waiting = new Map();
const errors = [];
const dialogs = [];

function send(method, params = {}) {
    const id = ++seq;
    ws.send(JSON.stringify({ id, method, params }));
    return new Promise((resolve, reject) => waiting.set(id, m => (m.error ? reject(new Error(method + ': ' + m.error.message)) : resolve(m.result))));
}

async function connect() {
    let port;
    for (let i = 0; i < 100 && !port; i++) {
        await sleep(100);
        try { port = fs.readFileSync(path.join(tmp, 'DevToolsActivePort'), 'utf8').split('\n')[0] }
        catch { /* not written yet */ }
    }
    if (!port) throw new Error('Chrome did not start');
    let targets;
    for (let i = 0; i < 50 && !targets?.some(t => t.type === 'page'); i++) {
        targets = await fetch(`http://127.0.0.1:${port}/json/list`).then(r => r.json()).catch(() => null);
        if (!targets) await sleep(100);
    }
    ws = new WebSocket(targets.find(t => t.type === 'page').webSocketDebuggerUrl);
    await new Promise(r => ws.addEventListener('open', r, { once: true }));
    ws.addEventListener('message', ev => {
        const msg = JSON.parse(ev.data);
        if (msg.id && waiting.has(msg.id)) {
            waiting.get(msg.id)(msg);
            waiting.delete(msg.id);
            return;
        }
        if (msg.method === 'Runtime.exceptionThrown') errors.push(msg.params.exceptionDetails.exception?.description || msg.params.exceptionDetails.text);
        if (msg.method === 'Runtime.consoleAPICalled' && msg.params.type === 'error') errors.push(msg.params.args.map(a => a.value ?? a.description).join(' '));
        if (msg.method === 'Log.entryAdded' && msg.params.entry.level === 'error' && !/favicon/.test(msg.params.entry.url || '')) errors.push(msg.params.entry.text + ' ' + (msg.params.entry.url || ''));
        if (msg.method === 'Page.javascriptDialogOpening') {
            dialogs.push(msg.params.message);
            send('Page.handleJavaScriptDialog', { accept: true });
        }
    });
}

async function js(expr) {
    const r = await send('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error(`${expr}: ${r.exceptionDetails.exception?.description || r.exceptionDetails.text}`);
    return r.result.value;
}

async function waitFor(expr, ms = 3000) {
    for (const t0 = Date.now(); Date.now() - t0 < ms; await sleep(50)) {
        if (await js(expr).catch(() => false)) return true;
    }
    return false;
}

async function shot(name) {
    if (!shots) return;
    const { data } = await send('Page.captureScreenshot', { format: 'png' });
    fs.mkdirSync(shots, { recursive: true });
    fs.writeFileSync(path.join(shots, name + '.png'), Buffer.from(data, 'base64'));
}

// A fresh document each time: navigating to a URL that differs only in its
// fragment would not reload the page.
async function load(hash = '') {
    await send('Page.navigate', { url: 'about:blank' });
    await send('Page.navigate', { url: base + hash });
    return waitFor(`document.querySelector('#summary h2') && document.querySelectorAll('section.commit .card').length > 0`);
}

const q = sel => `document.querySelector(${JSON.stringify(sel)})`;
const click = sel => js(`${q(sel)}.click()`);
// A card's widget button by its label.
const tool = (sel, label) => `[...document.querySelectorAll(${JSON.stringify(sel + ' .head .widgets button')})].find(b => b.textContent === '${label}')`;
// A commit's or file's tab by its label, and the labels of its tabs, the
// shown one starred when it alone is active, aria-checked and in the tab
// order.
const tab = (sel, label) => `[...document.querySelectorAll(${JSON.stringify(sel + ' .head .tabs button')})].find(b => b.textContent === '${label}')`;
const tabsOf = sel => js(`[...${q(sel + ' > .head .tabs[role=radiogroup]')}.children].map(b => b.textContent + (b.getAttribute('role') !== 'radio' ? '?'
    : b.classList.contains('active') && b.getAttribute('aria-checked') === 'true' && b.tabIndex === 0 ? '*'
    : b.classList.contains('active') || b.getAttribute('aria-checked') !== 'false' || b.tabIndex !== -1 ? '?' : '')).join('|')`);
// An arrow key pressed on the focused element.
const arrow = key => js(`document.activeElement.dispatchEvent(new KeyboardEvent('keydown', { key: '${key}', bubbles: true }))`);
const hash = () => js('location.hash');
// One key of the view state in the URL fragment.
const param = k => `new URLSearchParams(location.hash.slice(1)).get(${JSON.stringify(k)})`;
const exportData = () => js(`fetch('api/feedback').then(r => r.json())`);
// The flag a jump marked: the elements marked (each matching one of sels and
// marked in color), its one callout (holding text) placed by where(callout),
// shown in the top half of the screen with no stuck head over it, pulsing.
const focused = (sels, color, text, where) => js(`(() => {
    const marks = [...document.querySelectorAll('[data-flag-focus]')];
    const want = ${JSON.stringify(sels)}.map(s => document.querySelector(s));
    const calls = document.querySelectorAll('.flag-callout');
    const c = calls[0];
    const r = c?.getBoundingClientRect();
    const hit = r && document.elementFromPoint(r.left + 20, r.top + 4);
    const out = {
        marks: marks.length === want.length && want.every(m => marks.includes(m)) && marks.every(m => m.dataset.flagFocus === ${JSON.stringify(color)}),
        callout: calls.length === 1 && c.textContent.includes(${JSON.stringify(text)}) && (${where})(c),
        shown: !!(hit && c.contains(hit)) && r.top < innerHeight / 2,
        pulse: !!c?.classList.contains('flag-arrive'),
    };
    return Object.values(out).every(Boolean) || out;
})()`);
// A whole-file or commit flag a jump marked: its line on the card (sel), and
// nothing else, holding text, in the top half of the screen with no stuck
// head over it, pulsing; no callout.
const focusedLine = (sel, color, text) => js(`(() => {
    const marks = [...document.querySelectorAll('[data-flag-focus]')];
    const li = document.querySelector(${JSON.stringify(sel)});
    const r = li?.getBoundingClientRect();
    const hit = r && document.elementFromPoint(r.left + 20, r.top + 4);
    const out = {
        marks: marks.length === 1 && marks[0] === li && li.dataset.flagFocus === ${JSON.stringify(color)},
        text: !!li?.textContent.includes(${JSON.stringify(text)}) && !document.querySelector('.flag-callout'),
        shown: !!(hit && li.contains(hit)) && r.top < innerHeight / 2,
        pulse: !!li?.classList.contains('flag-arrive'),
    };
    return Object.values(out).every(Boolean) || out;
})()`);
// A card's own line for flag id.
const cardFlag = (card, id) => `${card} > .flag-slot > ul.card-flags > li[data-card-flag="${id}"]`;
const unfocusedExpr = `!document.querySelector('[data-flag-focus], .flag-callout')`;
const flagLink = start => js(`[...document.querySelectorAll('#summary .flag-list button')].find(b => b.textContent.startsWith(${JSON.stringify(start)})).click()`);
// The callout of lines sits in a row just above the first, of a head just under it.
const aboveRow = sel => `c => c.closest('tr').nextElementSibling === ${q(sel)}`;
const underHead = sel => `c => c.previousElementSibling === ${q(sel)}`;
// The element before n, skipping a card's empty flag list (drawn when it has
// flags to show).
const prevShown = n => `(n => { let p = n.previousElementSibling; while (p?.matches('.flag-slot:empty')) p = p.previousElementSibling; return p })(${n})`;
// Delay file fid's whole diff, and only it, so its panel stays "Loading…"
// for a while.
const slowDiff = fid => js(`(f => { window.fetch = (u, ...a) => /^api\\/diff\\/${fid}(\\?|$)/.test(String(u))
    ? new Promise(r => setTimeout(r, 700)).then(() => f(u, ...a)) : f(u, ...a) })(window.fetch)`);

// The squashed review split into topics: each topic shows where commits do,
// labelled with its name and listing its own files, flags and feedback.
async function driveTopics() {
    const R = await js(`fetch('api/review').then(r => r.json())`);
    const [perl, front] = R.commits;
    const label = id => js(`${q(`#c${id} .card > .head h2`)}?.textContent`);
    const files = id => js(`[...document.querySelectorAll('#c${id} .files .file .toggle')].map(b => b.textContent.replace(/^[▸▾] /, '')).join()`);
    check('two topics, two entries', R.commits.length === 2 && await js(`document.querySelectorAll('section.commit').length`) === 2, R.commits.map(c => c.topic));
    check('each labelled with its name', await label(perl.id) === '1. Perl backend' && await label(front.id) === '2. Frontend', [await label(perl.id), await label(front.id)]);
    check('each lists its files', await files(perl.id) === 'lib/Bar.pm,lib/Foo.pm' && await files(front.id) === 'js/util.js,share/icon.svg', [await files(perl.id), await files(front.id)]);
    check('with its own text', await js(`${q(`#c${front.id}`)}.textContent.includes('The page script and its icon.')`));
    const fUtil = front.files.find(f => f.path === 'js/util.js').id;
    check('a flag shows on its topic\'s file', await js(`!!${q(`#f${fUtil} .badge`)} && !${q(`#c${perl.id} .badge`)}`));
    check('the topic\'s feedback shows under it alone', await js(`${q(`#c${front.id} .notes`)}.textContent.includes('Is the icon final?') && !${q(`#c${perl.id} .notes`)}.textContent.includes('Is the icon final?')`));
    await click('#tree_open');
    check('the tree lists the topics', await waitFor(`(t => t.includes('1. Perl backend') && t.includes('2. Frontend'))(${q('#tree')}.textContent)`));

    await js(`[...document.querySelectorAll('#c${perl.id} .card > .head .widgets button')].find(b => b.textContent === 'feedback').click()`);
    await waitFor(`!!${q(`#c${perl.id} .form-slot form textarea`)}`);
    await js(`${q(`#c${perl.id} .form-slot form textarea`)}.value = 'Split Bar?'; ${q(`#c${perl.id} .form-slot form`)}.requestSubmit()`);
    check('feedback on a topic is stored on it', await waitFor(`fetch('api/feedback').then(r => r.json()).then(x => x.feedback.some(f => f.body === 'Split Bar?' && f.target === 'commit' && f.topic === 'Perl backend'))`)
        && await waitFor(`${q(`#c${perl.id} .notes`)}.textContent.includes('Split Bar?')`));
    check('feedback on the whole change before the split is in the summary, noting so',
        await js(`(t => t.includes('Review the whole app.') && t.includes('was on: the whole change'))(${q('#summary')}.textContent)`));

    await click('#guide_play');
    check('a guide step on a topic opens it', await waitFor(`${param('g')} === '1' && ${param('at')} === 'c${front.id}'`), await hash());
    check('a guide in one topic has no topic jumps, named for topics', await js(`[${q('#guide_prev_topic')}, ${q('#guide_next_topic')}].every(b => b.hidden)
        && ${q('#guide_next_topic')}.textContent === 'next topic »' && ${q('#guide_prev_topic')}.textContent === '« previous topic'`));
    await click('#guide_next');
    const flagRow = `#c${front.id} .panel table.code-view[data-fid="${fUtil}"] tr[data-new="4"]`;
    check('the next step shows the topic\'s flag', await waitFor(`${param('g')} === '2' && ${param('at')} === 'f${fUtil}' && !!${q(flagRow)}`), await hash());
    const step2 = await hash();
    check('reload restores the step in its topic', await load(step2)
        && await waitFor(`${q('#guide_count')}.textContent === '2 / 2' && !!${q(flagRow)}`), await hash());
    await click('#guide_exit');
    await shot('topics');
    check('no stray null text', !(await js(`document.body.innerText.includes('null')`)));
    check('no runtime errors', errors.length === 0, errors);
}

try {
    await connect();
    await send('Runtime.enable');
    await send('Page.enable');
    await send('Log.enable');
    await send('Page.setDownloadBehavior', { behavior: 'allow', downloadPath: tmp });

    await send('Page.navigate', { url });
    check('the printed URL opens the page', await waitFor(`location.pathname === '/' && !!document.querySelector('#summary h2')`), await js('location.href'));
    if (topics) {
        await driveTopics();
        ws?.close();
        console.log(`1..${count}`);
        await finish(failed ? 1 : 0);
    }

    // Ids come from the generated review.
    const R = await js(`fetch('api/review').then(r => r.json())`);
    const commit = subject => R.commits.find(c => c.subject === subject);
    const fileOf = (c, p) => c.files.find(f => f.path === p).id;
    const flagOf = start => R.flags.find(f => f.sentence.startsWith(start)).id;
    const sections = async fid => (await js(`fetch('api/file/${fid}').then(r => r.json())`)).sections;
    const sectionOf = async (fid, name) => (await sections(fid)).find(s => s.name === name).id;

    const C1 = commit('Add Foo, a logo and some data');
    const C2 = commit('Greet by name; add util.js');
    const C3 = commit('Drop shout');
    const C7 = commit("Merge branch 'docs'");
    const [c1, c2, c3, c4, c5, c6, c7, c8, c9] = [C1, C2, C3, commit('Add a README'), commit('Start a changelog'),
        commit("Merge branch 'changelog' into docs"), C7, commit('Move the notes'), commit('Add Bar, a counter')].map(c => c.id);
    const f1 = fileOf(C1, 'lib/Foo.pm');
    const f4 = fileOf(C2, 'lib/Foo.pm');
    const f5 = fileOf(C2, 'js/util.js');
    const f7 = fileOf(C3, 'js/util.js');
    const f8 = fileOf(commit('Add a README'), 'README.md');
    const f9 = fileOf(commit('Start a changelog'), 'Changes.md');
    const fTodo = fileOf(C3, 'TODO.txt');
    const fOldBin = fileOf(C3, 'old.bin');
    const s22 = await sectionOf(f4, 'sub name');
    const s23 = await sectionOf(f4, 'sub greet');
    const s51 = (await sections(f8))[0].id;
    const flag1 = flagOf('greet returns');
    const flag5 = flagOf('A test showed');
    const flag7 = flagOf('The changelog starts');
    const step5 = `#g=5&at=s${s22}&c${c2}=f${f4},s${s22}~d`;

    check('page loads', await load());

    // ---- the navigation tree's tabs ----
    // The tree starts closed; a tab opens it and one on its edge closes it,
    // and a reload keeps what the reader chose. Left open for what follows.
    const onScreen = sel => `(n => !!n && n.checkVisibility())(${q(sel)})`;
    const treeState = () => js(`[document.body.classList.contains('tree-hidden'), ${onScreen('#tree')}, ${onScreen('#tree_open')}, ${onScreen('#tree_close')}]`);
    check('the tree starts closed, its open tab shown', JSON.stringify(await treeState()) === '[true,false,true,false]', await treeState());
    check('no header button for the tree', await js(`!${q('#tree_toggle')} && ![...document.querySelectorAll('#header button')].some(b => b.textContent.includes('☰'))`));
    check('the open tab is labelled and says what it controls', await js(`(b => b.textContent === '›' && b.getAttribute('aria-label') === 'Show navigation' && b.getAttribute('aria-controls') === 'tree' && b.getAttribute('aria-expanded') === 'false')(${q('#tree_open')})`));
    // A tab is three times its first 40px, centred between the header and
    // the guide bar (or the screen's bottom), its glyph centred in it, and
    // left of every card and sticky head.
    const tabFitExpr = sel => `(b => {
        const t = b.getBoundingClientRect();
        const g = (r => { r.selectNodeContents(b); return r.getBoundingClientRect() })(document.createRange());
        const mid = r => [(r.left + r.right) / 2, (r.top + r.bottom) / 2];
        const top = ${q('#header')}.getBoundingClientRect().bottom;
        const bar = ${q('#guide_bar')};
        const bottom = bar.hidden ? document.documentElement.clientHeight : bar.getBoundingClientRect().top;
        const content = Math.min(...[...document.querySelectorAll('#summary, #main section.commit')].map(n => n.getBoundingClientRect().left));
        const out = {
            tall: Math.abs(t.height - 120) <= 1,
            centred: Math.abs(mid(t)[1] - (top + bottom) / 2) <= 2,
            glyph: g.width > 0 && Math.abs(mid(g)[0] - mid(t)[0]) <= 3 && Math.abs(mid(g)[1] - mid(t)[1]) <= 3,
            clear: t.right <= content,
            bar: t.bottom <= bottom,
        };
        return Object.values(out).every(Boolean) || { out, t: [t.top, t.bottom, t.right], top, bottom, content };
    })(${q(sel)})`;
    const tabFit = async sel => (await waitFor(`${tabFitExpr(sel)} === true`)) || js(tabFitExpr(sel));
    check('the open tab is tall, centred, and covers no card', await tabFit('#tree_open') === true, await tabFit('#tree_open'));
    await click('#tree_open');
    check('the close tab is tall, centred, and covers no card', await tabFit('#tree_close') === true, await tabFit('#tree_close'));
    check('the tab opens the tree, its close tab on its right edge, focused', JSON.stringify(await treeState()) === '[false,true,false,true]'
        && await js(`(b => document.activeElement === b && b.textContent === '‹' && b.getAttribute('aria-label') === 'Hide navigation' && b.getAttribute('aria-expanded') === 'true'
            && Math.abs(b.getBoundingClientRect().left - ${q('#tree')}.getBoundingClientRect().right) <= 2 && b.getBoundingClientRect().right <= ${q('#summary')}.getBoundingClientRect().left)(${q('#tree_close')})`), await treeState());
    await load();
    check('a reload keeps it open', JSON.stringify(await treeState()) === '[false,true,false,true]', await treeState());
    await click('#tree_close');
    check('the close tab closes it', JSON.stringify(await treeState()) === '[true,false,true,false]' && await js(`document.activeElement === ${q('#tree_open')}`), await treeState());
    await load();
    check('a reload keeps it closed', JSON.stringify(await treeState()) === '[true,false,true,false]', await treeState());
    await click('#tree_open');
    await load();

    // ---- the header and summary ----
    check('title and closed state', await js(`document.getElementById('title').textContent`) === 'Fixture review');
    check('source link', await js(`!${q('#source')}.hidden && ${q('#source')}.href`) === 'https://example.com/org/repo/pull/7');
    check('play in header', await js(`!${q('#guide_play')}.hidden`));
    check('the guide plays from the header only, not the summary', await js(`![...document.querySelectorAll('#summary button')].some(b => /guide/i.test(b.textContent))`));
    check('iteration note in summary, paragraphs and bullets like the rest', JSON.stringify(await js(`[...${q('#summary .note')}.children].map(c => [c.tagName, c.textContent])`))
        === JSON.stringify([['P', 'Drops shout after feedback.'], ['UL', 'Keeps greet.']]));
    check('flag list: every flag', await js(`document.querySelectorAll('#summary .flag-list li').length`) === R.flags.length);
    check('review feedback in summary', await js(`${q('#summary .notes')}.textContent.includes('why the logo')`));
    check('unplaced feedback in summary', await js(`${q('#summary .notes')}.textContent.includes('Not shown in this iteration') && ${q('#summary .notes')}.textContent.includes('js/old.js')`));
    check('section feedback on a section not shown is in the summary', await js(`(t => t.includes('Was sub gone needed?') && t.includes('sub gone'))(${q('#summary .notes')}.textContent)`));
    check('commit badge: its flags, in the worst color, a dot for unseen ones',
        await js(`(b => b && b.classList.contains('flag-red') && !!b.querySelector('.unseen-dot') && b.textContent)(${q(`li[data-node="c${c2}"] > .node .badge`)})`) === '4');
    check('commit feedback with status and response', await js(`(n => n.textContent.includes('declined') && n.textContent.includes('removed in the next commit'))(${q(`#c${c1} .notes`)})`));
    check('commit request', await js(`${q(`#c${c3} .notes`)}.textContent.includes('Debate requested')`));
    check('speech toggle always shown, off by default; the speech menu is in the header', await js(`!${q('#speech_toggle')}.hidden && ${q('#speech_toggle')}.getAttribute('aria-pressed') === 'false'
        && !!${q('#header #speech_menu #speech_voice')} && !${q('#guide_bar select, #guide_bar input[type=range]')}`));
    check('prose blocks have read-aloud buttons; short fields none', await js(`[...document.querySelectorAll('#summary > .prose-head')].filter(h => h.querySelector(':scope > .speak') && !h.querySelector('h3 .speak')).map(h => h.querySelector('h3').textContent).join()`) === 'What,Why,How'
        && await js(`!!${q('#summary > .speak-row .speak')} && !${q('#summary .facts .speak')}`));
    check('the guide entry is a labelled button', await js(`${q('#guide_play')}.textContent.includes('Play guide')`));
    // The header's controls: grouped by purpose, a line between the groups,
    // every control one height, and Close review a plain button at the far end.
    const headerLook = `(() => {
        const controls = [...document.querySelectorAll('#header .play, #header .switch, #header .head-btn, #header .head-menu > summary, #iteration')].filter(e => e.checkVisibility());
        const close = ${q('#close_review')}, last = ${q('#head_controls')}.lastElementChild;
        const style = getComputedStyle(close);
        return { groups: [...${q('#head_controls')}.children].filter(e => e.classList.contains('hgroup')).map(g => g.id).join(),
            line: getComputedStyle(${q('#head_view')}).borderLeftStyle, heights: [...new Set(controls.map(e => Math.round(e.getBoundingClientRect().height)))],
            closeLast: last === close, closeEnd: Math.abs(close.getBoundingClientRect().right - ${q('#head_controls')}.getBoundingClientRect().right) < 1,
            closeColor: style.color, closeBorder: style.borderTopStyle };
    })()`;
    const headLook = await js(headerLook);
    const grey = c => (v => Math.max(...v) - Math.min(...v) < 40)(c.match(/\d+/g).slice(0, 3).map(Number));
    check('header controls: grouped, one height, Close review a plain grey button at the far end', headLook.groups === 'head_nav,head_view' && headLook.line === 'solid'
        && headLook.heights.length === 1 && headLook.closeLast && headLook.closeEnd && headLook.closeBorder === 'solid' && grey(headLook.closeColor), headLook);
    check('guide bar hidden outside guide mode', await js(`${q('#guide_bar')}.hidden`));
    // The identity and title come first, in full, with room to read; the
    // controls follow on the same line when they fit, else on a second one.
    // Everything under the header starts at its real height.
    const headerFit = `(h => {
        const box = s => ${q('#header')}.querySelector(s).getBoundingClientRect();
        const [id, title, ctl] = ['#identity', '#title', '#head_controls'].map(box);
        const full = e => e.scrollWidth <= e.clientWidth;
        return { width: innerWidth, height: h.offsetHeight, headerH: getComputedStyle(document.documentElement).getPropertyValue('--header-h').trim(),
            treeTop: parseFloat(getComputedStyle(${q('#tree')}).top), id: [id.width, full(${q('#identity')})], title: [title.width, full(${q('#title')})],
            sameLine: Math.abs(id.top - title.top) < 8, oneLine: ctl.top < id.bottom, below: ctl.top >= Math.max(id.bottom, title.bottom) }
    })(${q('#header')})`;
    const fits = h => h.id[0] > 100 && h.id[1] && h.title[0] > 50 && h.title[1] && h.sameLine && h.headerH === h.height + 'px' && Math.abs(h.treeTop - h.height) < 1;
    let header = await js(headerFit);
    check('at desktop width the identity and title show in full on the first line, the controls beside them or below',
        header.width === 1280 && fits(header) && (header.oneLine ? header.height <= 52 : header.below && header.height <= 90), header);
    await send('Emulation.setDeviceMetricsOverride', { width: 1920, height: 900, deviceScaleFactor: 1, mobile: false });
    header = await waitFor(`(h => h.width === 1920 && h.headerH === h.height + 'px')(${headerFit})`) && await js(headerFit);
    check('on a wide screen the header is one line', !!header && fits(header) && header.oneLine && header.height <= 52, header);
    // Between the groups on one row, a line; the second group wrapped to a
    // row of its own (a mid-width window) starts with none.
    const divider = `(v => ({ width: innerWidth, wrapped: v.offsetTop > ${q('#head_nav')}.offsetTop + 5, color: getComputedStyle(v).borderLeftColor }))(${q('#head_view')})`;
    const oneRow = await js(divider);
    await send('Emulation.setDeviceMetricsOverride', { width: 900, height: 900, deviceScaleFactor: 1, mobile: false });
    const twoRows = await waitFor(`(d => d.width === 900 && d.wrapped && d.color === 'rgba(0, 0, 0, 0)')(${divider})`) && await js(divider);
    check('the line between the header groups shows only between them on one row', !oneRow.wrapped && oneRow.color !== 'rgba(0, 0, 0, 0)' && !!twoRows,
        { oneRow, twoRows: twoRows || await js(divider) });
    await send('Emulation.setDeviceMetricsOverride', { width: 1920, height: 900, deviceScaleFactor: 1, mobile: false });
    await js(`document.getElementById('title').textContent = 'A title far too long to fit beside the controls on one line, '.repeat(2)`);
    header = await waitFor(`(h => !h.oneLine && h.headerH === h.height + 'px')(${headerFit})`) && await js(headerFit);
    check('a title too long for one line moves the controls to a second line, and the page under the header follows it',
        !!header && header.below && header.headerH === header.height + 'px' && Math.abs(header.treeTop - header.height) < 1, header);
    await send('Emulation.clearDeviceMetricsOverride');
    await load();
    check('header identity follows the server', await js(`(i => [i.hidden, i.textContent])(${q('#identity')})`).then(([h, t]) => h === !identityLine(R.identity) && t === (identityLine(R.identity) ?? '')));
    await shot('01-summary');

    // ---- the tab's review ----
    check('the tab remembers its review, with no warning', await js(`!${q('#stale')}.hidden || JSON.parse(sessionStorage.getItem('review-helper.review')).id`) === R.review.id);
    await js(`sessionStorage.setItem('review-helper.review', JSON.stringify({ id: 'other-review', title: 'Other' }))`);
    await load();
    await shot('01c-stale-tab');
    check('a tab that showed another review warns', await js(`!${q('#stale')}.hidden && ${q('#stale_text')}.textContent`)
        === `This tab was showing review "Other" (other-review); this server is now serving review "Fixture review" (${R.review.id}).`);
    await click('#stale_accept');
    check('accepting continues with this review', await waitFor(`${q('#stale')}.hidden && JSON.parse(sessionStorage.getItem('review-helper.review')).id === '${R.review.id}'`));
    await load();
    check('and the warning stays gone', await js(`${q('#stale')}.hidden`));

    // ---- the summary's flag list ----
    // Grouped by color, red first.
    const groups = `[...document.querySelectorAll('#summary details.flag-group')]`;
    const groupText = ([color, meaning]) => (l => `⚑ ${color[0].toUpperCase() + color.slice(1)}: ${meaning} Walk new (${l.length})Walk all (${l.length})`)(R.flags.filter(f => f.color === color));
    check('fixture: flags of both colors', ['red', 'yellow'].every(c => R.flags.some(f => f.color === c)));
    check('flag groups: red first, collapsed, each saying what its color means, with its walks and their counts',
        JSON.stringify(await js(`${groups}.map(d => [[...d.classList].find(c => c.startsWith('flag-') && c !== 'flag-group'), d.open, d.querySelector('summary').textContent.trim()])`))
        === JSON.stringify(FLAG_COLORS.map(c => ['flag-' + c[0], false, groupText(c)])),
        await js(`${groups}.map(d => d.querySelector('summary').textContent)`));
    check("each group's icon says its color on hover", JSON.stringify(await js(`${groups}.map(d => d.querySelector('summary .flag-icon').title)`))
        === JSON.stringify(['Red flags, probably a problem', 'Yellow flags, a reviewer may object']));
    check('no number shows on or beside any flag icon', await js(`[...document.querySelectorAll('.flag-icon')].every(i => i.textContent === '⚑' && !/\\d/.test(i.nextSibling?.textContent?.trim()?.[0] ?? ''))`));
    check('collapsed groups hide their flags', await js(`[...document.querySelectorAll('#summary .flag-list li')].every(li => !li.checkVisibility())`));
    check('a group in commit order', JSON.stringify(await js(`[...${q('#summary details.flag-group.flag-red')}.querySelectorAll('li .link')].map(b => b.textContent.slice(0, 12))`))
        === JSON.stringify(['greet return', 'The README w']));
    check("another reviewer's seen mark is listed", await js(`[...document.querySelectorAll('#summary .flag-list li')].some(li => li.textContent.includes('The README') && li.querySelector('.flag-marks')?.textContent === ' seen by Bob Other')`));
    check('tree badges count their flags', await js(`(b => b && b.classList.contains('flag-yellow') && b.textContent)(${q(`li[data-node="c${c8}"] > .node .badge`)}) === '1' && (b => b && b.classList.contains('flag-yellow') && b.textContent)(${q(`li[data-node="c${c1}"] > .node .badge`)}) === '3'`));
    await click('#summary details.flag-group.flag-red > summary');
    check('a group expands', await waitFor(`${q('#summary details.flag-group.flag-red')}.open && ${q('#summary details.flag-group.flag-red li')}.checkVisibility()`));
    await js(`${q('#summary details.flag-group.flag-red')}.scrollIntoView({ block: 'center' })`);
    await shot('01b-summary-flags');
    await load();
    check('an expanded group is remembered', await js(`${q('#summary details.flag-group.flag-red')}.open && !${q('#summary details.flag-group.flag-yellow')}.open`));
    await click('#summary details.flag-group.flag-red > summary');
    await waitFor(`!${q('#summary details.flag-group.flag-red')}.open`);

    check('no flag settings in the header', await js(`!${q('#flag_view')} && !${q('#flag_min')}`));
    check('a comment on a flag shows only in its popup', await js(`!${q('#summary .notes')}.textContent.includes('Is the markup tested?')`));

    // ---- the journal ----
    // The agent's journal entries: last on the page, closed, not flags.
    const journalSel = '#journal .journal-list > li > details';
    check('fixture: two journal entries, one on a file the iteration changed, one not', R.journal.length === 2
        && R.journal.some(e => e.path === 'lib/Foo.pm') && R.journal.some(e => e.path === 'docs/plan.txt'), R.journal);
    check('the journal is the last section of the page, said to be for your information, an entry each, closed',
        await js(`(j => !!j && !j.hidden && j === [...document.querySelectorAll('#main > section:not([hidden])')].at(-1) && j.querySelector('h2').textContent === 'Journal'
            && j.querySelector(':scope > p.meta').textContent.startsWith('For your information'))(${q('#journal')})`)
        && JSON.stringify(await js(`[...document.querySelectorAll(${JSON.stringify(journalSel)})].map(d => [d.open, d.querySelector('summary').textContent])`))
        === JSON.stringify(R.journal.map(e => [false, e.sentence])));
    check('a journal entry shows its disclosure arrow and no list bullet', await js(`[...document.querySelectorAll('#journal .journal-list > li')].every(li => getComputedStyle(li).listStyleType === 'none' && getComputedStyle(li.querySelector('summary')).display === 'list-item')`));
    check('journal entries are not flags: no flag icon, badge or walk count', await js(`!${q('#journal .flag-icon, #journal .badge')}`)
        && await js(`${q('#walk_all')}.textContent`) === `Walk all (${R.flags.length})`);
    const testsEntry = `[...document.querySelectorAll(${JSON.stringify(journalSel)})].find(d => d.textContent.includes('fresh object'))`;
    const planEntry = `[...document.querySelectorAll(${JSON.stringify(journalSel)})].find(d => d.textContent.includes('plan file'))`;
    await js(`${testsEntry}.querySelector('summary').click()`);
    check('an entry opens to its kind, file and unit, a read-aloud button and its detail', await waitFor(`(d => d.open && d.querySelector('p.meta')?.textContent.startsWith('test-revealed · lib/Foo.pm · greet')
        && !!d.querySelector('p.meta .speak .speak-play') && d.querySelectorAll('.prose p, p').length >= 3 && d.textContent.includes('Each test now builds its own.'))(${testsEntry})`),
        await js(`${testsEntry}.innerHTML`));
    await js(`${planEntry}.querySelector('summary').click()`);
    check('a file the iteration did not change is named, not linked', await waitFor(`(d => d.open && !d.querySelector('button.link') && d.querySelector('p.meta').textContent.startsWith('other · docs/plan.txt'))(${planEntry})`));
    await js(`${q('#journal')}.scrollIntoView({ block: 'end' })`);
    await shot('01e-journal');
    const lastFoo = R.commits.findLast(c => c.files.some(f => f.path === 'lib/Foo.pm'));
    const fooFile = lastFoo.files.find(f => f.path === 'lib/Foo.pm');
    await js(`${testsEntry}.querySelector('button.link').click()`);
    check("an entry's file link goes to the file in the last commit that changed it", await waitFor(`!!${q(`#f${fooFile.id}`)} && ${param('at')} === 'f${fooFile.id}'`), await hash());
    await load();
    await js(`${testsEntry}.querySelector('summary').click()`);
    await waitFor(`${testsEntry}.open`);
    await js(`${testsEntry}.querySelector('summary').click()`);
    check('a closed entry drops its details, so a reading of it pauses', await waitFor(`(d => !d.open && !d.querySelector('.speak'))(${testsEntry})`));

    // ---- guide ----
    await click('#guide_play');
    check('play enters step 1', await waitFor(`${param('g')} === '1' && !${q('#guide_bar')}.hidden`));
    check('step count', await js(`${q('#guide_count')}.textContent`) === `1 / ${R.guide.length}`);
    check('the close tab clears the guide bar', await tabFit('#tree_close') === true, await tabFit('#tree_close'));
    check('back disabled at step 1', await js(`${q('#guide_back')}.disabled`));
    await shot('02-guide-1');
    await click('#guide_next');
    check('next: step 2 opens commit 1', await waitFor(`${param('g')} === '2' && ${param('at')} === 'c${c1}'`));
    // The bar's controls stack in a block of rows right of its text, which
    // is the wider: reading and count, the jumps (or the walk's seen mark),
    // back and next, then replay, speech and exit.
    const barLayout = `(() => {
        const box = x => (typeof x === 'string' ? document.getElementById(x) : x).getBoundingClientRect();
        const rows = [...document.querySelectorAll('#guide_bar .controls > .crow')].filter(x => !x.hidden);
        const c = box(${q('#guide_bar .controls')}), t = box('guide_text');
        return { rows: rows.map(x => x.id).join(), beside: t.right <= c.left, wider: t.width > c.width,
            stacked: rows.every((x, i) => !i || box(rows[i - 1]).bottom <= box(x).top + 1),
            paired: Math.abs(box('guide_back').top - box('guide_next').top) < 1 && box('guide_back').right <= box('guide_next').left };
    })()`;
    const barOk = rows => `(l => l.rows === '${rows}' && l.beside && l.wider && l.stacked && l.paired)(${barLayout})`;
    check("the guide bar's controls stack in rows beside its text", await waitFor(barOk('guide_play_row,guide_topics,guide_steps,guide_more')), await js(barLayout));
    await click('#guide_next');
    check('step 3 opens the file', await waitFor(`${param('g')} === '3' && !!${q(`#f${f4}.card`)}`));
    check("step 3 keeps the commit's other files listed", await js(`document.querySelectorAll('#c${c2} .files > .file').length === ${C2.files.length} && document.querySelectorAll('#c${c2} .file.card').length === 1`));
    await click('#guide_next');
    check('step 4 opens the section', await waitFor(`${param('g')} === '4' && !!${q(`#s${s23}.card`)}`));
    check('step 4 shows the section diff', await waitFor(`!!${q(`#s${s23} .panel table.diff[data-sid="${s23}"]`)}`), await hash());
    check('step 4 shows its highlighted line, tinted and on screen', await waitFor(`(row => row && getComputedStyle(row.querySelector('td.code')).backgroundImage !== 'none'
        && (r => r.top >= 0 && r.bottom <= innerHeight)(row.getBoundingClientRect()))(${q(`#s${s23} .panel table.diff tr.hl[data-new="12"]`)})`), await hash());
    // Moving on closes what step 4 opened, and only that.
    await js(`${tab(`#c${c1}`, 'diff')}.click()`);
    await waitFor(`${param('c' + c1)} === 'd'`);
    await click('#guide_next');
    check('step 5 closes the section step 4 opened', await waitFor(`${param('g')} === '5' && !${q(`#s${s23}.card`)} && !!${q(`#s${s23} .toggle`)}`), await hash());
    check('a panel the reader opened stays open', await js(`${param('c' + c1)} === 'd' && !!${q(`#c${c1} .panel`)}`), await hash());
    check('step 5 highlights the diff line', await waitFor(`!!${q(`.panel table[data-fid="${f4}"] tr.hl[data-new="8"]`)}`), await hash());
    check('step 5 highlights nothing else', await js(`document.querySelectorAll('.hl').length`) === 1);
    // The tint layers over the row's own color, so an added line stays green.
    const hlStyle = `(t => (s => ({ img: s(t.querySelector('tr.hl td.code')).backgroundImage, color: s(t.querySelector('tr.hl td.code')).backgroundColor,
        plain: s(t.querySelector('tr:not(.hl):not(.hunk) td.code')).backgroundImage, bar: s(t.querySelector('tr.hl td')).boxShadow }))(getComputedStyle))(${q(`.panel table[data-fid="${f4}"]`)})`;
    const hl = await js(hlStyle);
    check('step 5 highlight is a warm tint over the added line, with a thick bar', hl.img.includes('rgba(255, 170, 0') && hl.plain === 'none' && hl.color === 'rgb(224, 255, 224)' && /6px/.test(hl.bar), hl);
    await shot('03-guide-5');
    await click('#guide_next');
    check('step 6 shows the flagged lines', await waitFor(`${param('g')} === '6' && !!${q(`.panel table.code-view[data-fid="${f4}"] .flag-icon[data-flag="${flag1}"]`)}`));
    check('step 6 closes the section diff step 5 opened', await js(`!${q(`#s${s22}.card`)}`), await hash());
    check('step 6 scrolls to the flagged line', await waitFor(`(r => r.top > 0 && r.bottom < innerHeight)(${q(`.panel table.code-view[data-fid="${f4}"] tr[data-new="10"]`)}.getBoundingClientRect())`),
        await js(`${q(`.panel table.code-view[data-fid="${f4}"] tr[data-new="10"]`)}.getBoundingClientRect().top`));
    check('highlight cleared on step change', await js(`document.querySelectorAll('.hl').length`) === 0);

    // flag popup on the step's file view
    const greetIcon = `.panel table.code-view[data-fid="${f4}"] .flag-icon[data-flag="${flag1}"]`;
    check("flag icon title gives its color's meaning and the sentence; a speech mark for its comment, no eye yet",
        await js(`(i => i.title === 'Red flag, probably a problem: greet returns a heredoc with markup, which the spec did not ask for. (comments)' && !!i.querySelector('.flag-mark.talk') && !i.querySelector('.flag-mark.eye'))(${q(greetIcon)})`),
        await js(`${q(greetIcon)}.title`));
    check('flagged rows carry the gutter bar in its color', await js(`document.querySelectorAll('.panel table.code-view[data-fid="${f4}"] td[data-flagged="flag-red"]').length`) === 5);
    await click(greetIcon);
    const popupText = () => js(`${q('.flag-popup')}?.textContent ?? ''`);
    check("flag popup: its color's meaning, kind, sentence, detail, the seen control, comments and a box; no unflag",
        await waitFor(`(p => p && ['Red flag, probably a problem', 'deviation', 'greet returns a heredoc', 'plain greeting', 'Is the markup tested?', 'Mark'].every(t => p.textContent.includes(t)) && !p.textContent.includes('Unflag') && !!p.querySelector('.flag-comment textarea'))(${q('.flag-popup')})`),
        await popupText());
    check('opening it marks the flag seen: an eye on its icon', await waitFor(`!!${q(greetIcon + ' .flag-mark.eye')} && ${q('.flag-popup')}.textContent.includes('Mark unseen')`), await popupText());
    // The close button stays on the row of the speech buttons, the title
    // wrapping beside them instead.
    const closeRow = `(p => p && (([a, b]) => Math.abs((a.top + a.bottom) / 2 - (b.top + b.bottom) / 2) < 3 && a.right <= b.left)([p.querySelector('.speak-stop'), p.querySelector('.close')].map(x => x.getBoundingClientRect())))(${q('.flag-popup')})`;
    check("the popup's close button shares the speech buttons' row", await js(closeRow), await js(`${q('.flag-popup .fb-head')}?.outerHTML`));
    await shot('04-flag-popup');
    await js(`(t => { t.value = 'Keep the markup.'; t.closest('form').requestSubmit() })(${q('.flag-popup .flag-comment textarea')})`);
    check('a comment added in the popup shows in its thread', await waitFor(`${q('.flag-popup')}?.textContent.includes('Keep the markup.')`), await popupText());
    check('and is feedback on the flag', (await exportData()).feedback.some(f => f.target === 'flag' && f.body === 'Keep the markup.' && f.flag?.note === R.flags.find(x => x.id === flag1).note_id));
    await js(`[...${q('.flag-popup')}.querySelectorAll('button')].find(b => b.textContent === 'Mark unseen').click()`);
    check('mark unseen: the eye goes', await waitFor(`!${q(greetIcon + ' .flag-mark.eye')} && ${q('.flag-popup')}.textContent.includes('Mark seen')`), await popupText());
    await click('#summary h2');
    check('popup closes on outside click', await waitFor(`!${q('.flag-popup')}`));
    // An icon left from before its flag went away opens nothing, and throws
    // nothing.
    const errorsBefore = errors.length;
    await js(`(i => { i.className = 'flag-icon'; i.dataset.flag = '-1'; ${q(greetIcon)}.after(i); i.click(); i.remove() })(document.createElement('span'))`);
    await sleep(100);
    check('a stale flag icon opens no popup and throws nothing', await js(`!${q('.flag-popup')}`) && errors.length === errorsBefore, errors.slice(errorsBefore));

    // Nothing typed in the popup is lost to a redraw or a reopen.
    const typeIn = (sel, text) => js(`(t => { t.value = ${JSON.stringify(text)}; t.dispatchEvent(new Event('input', { bubbles: true })) })(${q(sel)})`);
    const popupButton = label => js(`[...${q('.flag-popup')}.querySelectorAll('button')].find(b => b.textContent === ${JSON.stringify(label)}).click()`);
    const typed = () => js(`${q('.flag-popup .flag-comment textarea')}?.value`);
    await click(greetIcon);
    await waitFor(`${q('.flag-popup')}?.textContent.includes('Mark unseen')`);
    await typeIn('.flag-popup .flag-comment textarea', 'Back on the list');
    await popupButton('Mark unseen');
    check('a draft survives the popup redrawn by the seen toggle', await waitFor(`${q('.flag-popup')}.textContent.includes('Mark seen')`) && await typed() === 'Back on the list', await typed());
    await js(`document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))`);
    await waitFor(`!${q('.flag-popup')}`);
    await click(greetIcon);
    check('and closing and reopening it', await waitFor(`!!${q('.flag-popup .flag-comment textarea')}`) && await typed() === 'Back on the list', await typed());
    await js(`${q('.flag-popup .flag-comment textarea')}.closest('form').requestSubmit()`);
    check('sending clears it', await waitFor(`${q('.flag-popup .flag-comments')}.textContent.includes('Back on the list') && ${q('.flag-popup .flag-comment textarea')}.value === ''`), await typed());
    await js(`[...${q('.flag-popup .flag-comments')}.querySelectorAll('.fb')].find(f => f.textContent.includes('Back on the list')).querySelector('.fb-head button').click()`);
    await typeIn('.flag-popup .flag-comments .fb form textarea', 'Back on the list, edited');
    await popupButton('Mark unseen');
    check('a comment edit left open survives a redraw', await waitFor(`${q('.flag-popup')}.textContent.includes('Mark seen')`)
        && await js(`${q('.flag-popup .flag-comments .fb form textarea')}?.value`) === 'Back on the list, edited');
    await js(`document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))`);
    await waitFor(`!${q('.flag-popup')}`);
    await click(greetIcon);
    check('and a reopen', await waitFor(`${q('.flag-popup .flag-comments .fb form textarea')}?.value === 'Back on the list, edited'`));
    await js(`[...${q('.flag-popup .flag-comments .fb form')}.querySelectorAll('button')].find(b => b.textContent === 'Cancel').click()`);
    await waitFor(`${q('.flag-popup')}.textContent.includes('Mark unseen')`);
    await popupButton('Mark unseen');
    await waitFor(`${q('.flag-popup')}.textContent.includes('Mark seen')`);
    await click('#summary h2');
    await waitFor(`!${q('.flag-popup')}`);

    await click('#guide_next');
    check('step 7 view with highlight', await waitFor(`!!${q(`.panel table.code-view[data-fid="${f5}"] tr.hl[data-new="4"]`)}`));
    await click('#guide_next');
    check('step 8 commit diff with old-side highlight', await waitFor(`!!${q(`.panel table[data-fid="${f7}"] tr.hl[data-old="8"]`)}`));
    await click('#guide_next');
    check('step 9 opens the merges down to the nested commit', await waitFor(`${param('g')} === '9' && ${param('c' + c6)} === 'c${c5}' && ${param('c' + c7)} === 'c${c6}' && !!${q(`#c${c7} #c${c6} #c${c5}.card`)}`), await hash());
    await js(`${tab(`#c${c8}`, 'diff')}.click()`);
    await waitFor(`${param('c' + c8)} === 'd'`);
    await click('#guide_next');
    check('a whole-file step keeps the commit diff the reader opened', await waitFor(`!!${q(`#b${c8}`)} && ${param('c' + c8)}.split(',')[0] === 'd' && !!${q(`#c${c8} > .card > .panel table.diff`)}`), await hash());
    check('step 10 shows the file at the head with its highlight', await waitFor(`!!${q(`#b${c8} .panel table[data-path="lib/Foo.pm"] tr.hl[data-new="12"]`)}`), await hash());
    check('a whole-file view has no close button', await js(`!document.querySelector('#commits .tool.close') && ${q(`#b${c8} > .head`)}.classList.contains('collapsible')`));
    check('step 10 shows no flags of other commits', await js(`document.querySelectorAll('#b${c8} .flag-icon').length`) === 0);
    check('step 10 scrolls to the lines', await waitFor(`(r => r.top > 0 && r.bottom < innerHeight)(${q(`#b${c8} tr[data-new="10"]`)}.getBoundingClientRect())`));
    check('head view takes no line feedback', await js(`${q(`#b${c8} td.ln[data-side="new"]`)}.click(), !${q(`#b${c8} tr.fb-form`)}`));
    await shot('08-guide-head-view');
    // Step 11's first highlighted line is outside the diff's hunks: the page
    // still goes to the file.
    await click('#guide_next');
    check('step 11 highlights the lines the diff shows', await waitFor(`${param('g')} === '11' && !!${q(`.panel table[data-fid="${f4}"] tr.hl[data-new="8"]`)}`), await hash());
    check('step 11 scrolls to the file though its first line is not shown',
        await waitFor(`!${q(`.panel table[data-fid="${f4}"] tr[data-new="4"]`)} && (r => r.top >= -1 && r.top < innerHeight / 3)(${q(`#f${f4}`)}.getBoundingClientRect())`),
        await js(`${q(`#f${f4}`)}.getBoundingClientRect().top`));
    // Step 12's section is not shown (split away): its commentary only.
    await click('#guide_next');
    check('a step on a section not shown shows its commentary without an error', await waitFor(`${param('g')} === '12' && ${q('#guide_text')}.textContent === ${JSON.stringify(R.guide[11].commentary)} && ${q('#error')}.hidden`), await hash());
    check('next disabled at the last step', await js(`${q('#guide_next')}.disabled`));
    await click('#guide_back');
    check('back from it goes to step 11', await waitFor(`${param('g')} === '11'`));
    await click('#guide_back');
    check('back goes to step 10', await waitFor(`${param('g')} === '10' && ${q('#guide_count')}.textContent === '10 / 12'`));
    await js('history.back()');
    check('browser back follows steps', await waitFor(`${param('g')} === '11' && ${q('#guide_count')}.textContent === '11 / 12' && !!${q(`.panel table[data-fid="${f4}"] tr.hl`)}`));
    // exploring by hand keeps guide mode and the step
    await click(`li[data-node="c${c1}"] > .node > .link`);
    check('hand exploring keeps the step', await waitFor(`${param('at')} === 'c${c1}' && ${param('g')} === '11' && !${q('#guide_bar')}.hidden`));
    await click('#guide_back');
    check('back resumes from the current step', await waitFor(`${param('g')} === '10'`));
    await click('#guide_exit');
    check('exit leaves guide mode', await waitFor(`${param('g')} === null && ${q('#guide_bar')}.hidden && !document.querySelector('.hl')`));

    // ---- the guide's next and previous commit ----
    // Steps by commit: 1 the summary, 2 c1, 3-7 c2 (6 its flag), 8 c3, 9 c5,
    // 10 c8, then 11-12 c2 again.
    const jumpLabels = () => js(`[${q('#guide_prev_topic')}, ${q('#guide_next_topic')}].map(b => (b.hidden ? null : b.textContent))`);
    const atStep = n => waitFor(`${param('g')} === '${n}' && ${q('#guide_count')}.textContent === '${n} / ${R.guide.length}'`);
    const greetSeen = () => js(`fetch('api/review').then(r => r.json()).then(r => r.flags.find(f => f.id === ${flag1}).seen.length > 0)`);
    check('fixture: the guide\'s flag not seen', !(await greetSeen()));
    await click('#guide_play');
    check('a summary step: next commit, no previous', await atStep(1) && JSON.stringify(await jumpLabels()) === JSON.stringify([null, 'next commit »']), await jumpLabels());
    await click('#guide_next_topic');
    check('next commit from the summary goes to the first commit\'s step; none before it', await atStep(2) && JSON.stringify(await jumpLabels()) === JSON.stringify([null, 'next commit »']), await jumpLabels());
    await click('#guide_next_topic');
    await atStep(3);
    await click('#guide_next_topic');
    check('next commit passes over the rest of the commit, its flag step too', await atStep(8) && JSON.stringify(await jumpLabels()) === JSON.stringify(['« previous commit', 'next commit »']), await jumpLabels());
    check('the flag step passed over is not marked seen', !(await greetSeen()));
    await click('#guide_prev_topic');
    check('previous commit goes to the first step of the commit before', await atStep(3), await hash());
    for (const n of [8, 9, 10]) {
        await click('#guide_next_topic');
        await atStep(n);
    }
    await click('#guide_next_topic');
    check('a guide that comes back to a commit: its later steps are a commit of their own, with none after',
        await atStep(11) && JSON.stringify(await jumpLabels()) === JSON.stringify(['« previous commit', null]), await jumpLabels());
    await click('#guide_prev_topic');
    check('and previous goes to the commit before them', await atStep(10), await hash());
    await click('#guide_exit');
    await waitFor(`${param('g')} === null`);

    check('reload restores a step', await load(step5));
    check('restored step highlights', await waitFor(`${q('#guide_count')}.textContent === '5 / 12' && !!${q('tr.hl[data-new="8"]')}`));

    // ---- the flag walker ----
    const me = R.identity.reviewer;
    const markAll = on => js(`Promise.all(${JSON.stringify(R.flags.map(x => x.id))}.map(id => fetch('api/flag/' + id, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ seen: ${on} }) }).then(r => r.ok))).then(l => l.every(x => x))`);
    check('fixture: no flag seen by this reviewer', await markAll(false));
    const walk = walkSteps(R.flags, R.commits, me, 'f');
    const bar = () => js(`[${q('#guide_text')}.textContent, ${q('#guide_count')}.textContent, ${param('w')}, ${param('g')}, !${q('#walk_opts')}.hidden, ${q('#walk_mark')}.textContent, ${q('#walk_marks')}.textContent]`);
    const seenBy = id => js(`fetch('api/review').then(r => r.json()).then(r => r.flags.find(f => f.id === ${id}).seen.map(m => m.reviewer))`);
    // The walker marks a flag seen as it moves on, without waiting.
    const seenSoon = id => waitFor(`fetch('api/review').then(r => r.json()).then(r => r.flags.find(f => f.id === ${id}).seen.some(m => m.reviewer === ${JSON.stringify(me)}))`);
    check('the server names the reviewer', me === 'Tess Tester <tess@example.com>' && (await js(`${q('#identity')}.title`)).endsWith('reviewing as ' + me));
    check('fixture: one flag seen by another reviewer; every flag walked', walk.length === R.flags.length && R.flags.filter(f => f.seen.length).length === 1);
    await load();
    const walkLabels = sel => js(`[...document.querySelectorAll(${JSON.stringify(sel)})].map(b => b.textContent + (b.disabled ? ' off' : ''))`);
    check('walk new and walk all, with their counts, in the header and the summary', JSON.stringify(await walkLabels('#walk_start button'))
        === JSON.stringify([`⚑ Walk new (${walk.length})`, `Walk all (${walk.length})`]) && JSON.stringify(await walkLabels('#flag_list .head .play'))
        === JSON.stringify(await walkLabels('#walk_start button')), [await walkLabels('#walk_start button'), await walkLabels('#flag_list .head .play')]);
    await click('#walk_new');
    // While walking, g is the flag's id.
    const gOf = i => String(walk[i].flag);
    check('a walk has no topic jumps', await js(`${q('#guide_prev_topic')}.hidden && ${q('#guide_next_topic')}.hidden`));
    check("the walk's controls stack the same way, its seen mark in the jumps' place", await waitFor(barOk('guide_play_row,walk_opts,guide_steps,guide_more')), await js(barLayout));
    // Who else has seen a flag wraps under the seen mark; it never widens
    // the block.
    const blockWidth = `${q('#guide_bar .controls')}.getBoundingClientRect().width`;
    const narrowBlock = await js(blockWidth);
    const marksWere = await js(`${q('#walk_marks')}.textContent`);
    await js(`${q('#walk_marks')}.textContent = 'Seen by Ann Other <ann@example.com>, Bob Other <bob@example.com>, Cy Third <cy@example.com>'`);
    check("other reviewers' seen marks wrap in the block, its width unchanged", await js(`${blockWidth} === ${narrowBlock}`) && await js(barOk('guide_play_row,walk_opts,guide_steps,guide_more'))
        && await js(`${q('#walk_marks')}.getBoundingClientRect().height > 20`), [narrowBlock, await js(blockWidth), await js(barLayout)]);
    await js(`${q('#walk_marks')}.textContent = ${JSON.stringify(marksWere)}`);
    check('walking starts at the first flag, red first', await waitFor(`${param('w')} === 'f' && ${param('g')} === '${gOf(0)}' && !${q('#walk_opts')}.hidden`)
        && JSON.stringify(await bar()) === JSON.stringify([walk[0].commentary, `1 / ${walk.length}`, 'f', gOf(0), true, 'Mark seen', '']), await bar());
    const shows0 = `!!${q(`.panel table.code-view[data-fid="${f4}"] tr[data-new="10"]`)}`;
    check('the flag is shown, as a guide flag step shows it', await waitFor(shows0), await hash());
    const greetRows = [10, 11, 12, 13, 14].map(n => `.panel table.code-view[data-fid="${f4}"] tr[data-new="${n}"]`);
    await waitFor(`!!${q('.flag-callout')}`);
    check('the walker marks its flag\'s lines, its sentence above them, in view',
        await focused(greetRows, 'flag-red', walk[0].commentary, aboveRow(greetRows[0])) === true, await focused(greetRows, 'flag-red', walk[0].commentary, aboveRow(greetRows[0])));
    check('arriving at a flag does not mark it seen', JSON.stringify(await seenBy(walk[0].flag)) === '[]');
    await shot('13a-flag-walk-focus');
    await click('#guide_next');
    const readme = walk[1];
    const f8line = cardFlag(`#f${f8}`, readme.flag);
    check('the next step marks its whole-file flag on its line on the file card, and only it',
        await waitFor(`${param('g')} === '${String(readme.flag)}' && !!${q(f8line + '[data-flag-focus]')}`)
        && await focusedLine(f8line, 'flag-red', readme.commentary) === true, await focusedLine(f8line, 'flag-red', readme.commentary));
    check("the next flag; another reviewer's seen mark shown", await waitFor(`${q('#guide_text')}.textContent === ${JSON.stringify(readme.commentary)}`)
        && (await bar())[6] === 'Seen by Bob Other', await bar());
    check('moving on marked the flag left seen', await seenSoon(walk[0].flag) && JSON.stringify(await seenBy(walk[0].flag)) === JSON.stringify([me]));
    await click('#guide_back');
    await waitFor(`${param('g')} === '${gOf(0)}'`);
    await waitFor(`!!${q(greetRows[0] + '[data-flag-focus]')}`);
    check('back marks the earlier flag again, and the one left seen too', await focused(greetRows, 'flag-red', walk[0].commentary, aboveRow(greetRows[0])) === true
        && await seenSoon(readme.flag), await focused(greetRows, 'flag-red', walk[0].commentary, aboveRow(greetRows[0])));
    check('a seen flag: the button says mark unseen, the icon has an eye', await waitFor(`${q('#walk_mark')}.textContent === 'Mark unseen' && !!${q(greetRows[0] + ' .flag-icon .flag-mark.eye')}`), await bar());
    await click('#walk_mark');
    check('mark unseen: the flag stays, the button says mark seen', await waitFor(`${q('#walk_mark')}.textContent === 'Mark seen'`) && (await bar())[0] === walk[0].commentary);
    check('the mark is gone for this reviewer', JSON.stringify(await seenBy(walk[0].flag)) === '[]');
    // Marked unseen by hand, it stays unseen when the walk moves on.
    await click('#guide_next');
    await waitFor(`${param('g')} === '${String(readme.flag)}'`);
    await click('#guide_back');
    await waitFor(`${param('g')} === '${gOf(0)}' && ${q('#walk_mark')}.textContent === 'Mark seen'`);
    await js('new Promise(r => setTimeout(r, 300))');
    check('a flag marked unseen by hand stays unseen as the walk moves on and back', JSON.stringify(await seenBy(walk[0].flag)) === '[]' && (await bar())[5] === 'Mark seen', await bar());
    await click('#guide_next');
    await waitFor(`${param('g')} === '${String(readme.flag)}'`);
    await js('new Promise(r => setTimeout(r, 300))');
    check('and when the walk leaves it again: the choice holds for the rest of the walk', JSON.stringify(await seenBy(walk[0].flag)) === '[]');
    await click('#guide_back');
    await waitFor(`${param('g')} === '${gOf(0)}' && ${q('#walk_mark')}.textContent === 'Mark seen'`);
    await click('#walk_mark');
    check('mark seen again', await waitFor(`${q('#walk_mark')}.textContent === 'Mark unseen'`) && JSON.stringify(await seenBy(walk[0].flag)) === JSON.stringify([me]));
    check('the summary flag list shows it', await js(`[...document.querySelectorAll('#summary .flag-list li')].some(li => li.textContent.includes(${JSON.stringify(walk[0].commentary)}) && li.querySelector('.flag-marks')?.textContent === ' seen by you')`));
    await shot('13-flag-walk');
    check('after a reload the walk stays on the flag shown, though seen; the other seen one is left out', await load(await hash())
        && await waitFor(`${q('#guide_text')}.textContent === ${JSON.stringify(walk[0].commentary)} && ${q('#guide_count')}.textContent === '1 / ${walk.length - 1}' && ${q('#walk_mark')}.textContent === 'Mark unseen' && ${shows0}`), await bar());
    await click('#walk_new');
    check('walking new again starts at the first flag not seen, and the bar follows', await waitFor(`${param('g')} === '${gOf(2)}'`)
        && JSON.stringify(await bar()) === JSON.stringify([walk[2].commentary, `1 / ${walk.length - 2}`, 'f', gOf(2), true, 'Mark seen', '']), await bar());
    check('and closes what the kept flag opened', await waitFor(`!${shows0}`), await hash());
    const depth = await js('history.length');
    await js('history.back()');
    check('back to the kept flag shows it again', await waitFor(`${param('g')} === '${gOf(0)}' && ${q('#guide_text')}.textContent === ${JSON.stringify(walk[0].commentary)} && ${q('#walk_mark')}.textContent === 'Mark unseen'`)
        && await js(`history.length === ${depth}`), await bar());
    await js('history.forward()');
    await waitFor(`${param('g')} === '${gOf(2)}' && ${q('#guide_text')}.textContent === ${JSON.stringify(walk[2].commentary)}`);
    check("the browser's Back marked the flag it left seen", await seenSoon(walk[2].flag));
    // Unseen again, and the page reloaded to know it, so the check below
    // is on what back does.
    await js(`fetch('api/flag/${walk[2].flag}', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ seen: false }) })`);
    await load(await hash());
    await waitFor(`${param('g')} === '${gOf(2)}' && ${q('#walk_mark')}.textContent === 'Mark seen'`);
    check('no skip-seen switch in the walk', await js(`!${q('#walk_opts input')}`));
    await click('#walk_all');
    check('walk all starts at the first flag, red first, seen or not', await waitFor(`${param('w')} === 'a' && ${param('g')} === '${gOf(0)}'
        && ${q('#guide_count')}.textContent === '1 / ${walk.length}' && ${q('#guide_text')}.textContent === ${JSON.stringify(walk[0].commentary)}`), await bar());
    check('starting another walk marked the flag left seen', await seenSoon(walk[2].flag));
    await click('#guide_next');
    check('and shows the seen ones too', await waitFor(`${q('#guide_text')}.textContent === ${JSON.stringify(readme.commentary)} && ${q('#walk_mark')}.textContent === 'Mark unseen'`), await bar());
    await click('#guide_exit');
    check('exit leaves the walk', await waitFor(`${param('w')} === null && ${param('g')} === null && ${q('#guide_bar')}.hidden`));
    await load(`#w=f&g=999999&u=c${c2}~d`);
    check('a walk at a flag it does not have is left', await waitFor(`${param('w')} === null && ${param('g')} === null && ${q('#guide_bar')}.hidden`), await hash());
    check('leaving it undoes what its step changed', await waitFor(`${param('u')} === null && ${param('c' + c2)} === 'd'`), await hash());
    // The tab's history is capped (50 entries in Chrome): start it afresh,
    // so a new entry would show.
    await send('Page.resetNavigationHistory');
    const depth0 = await js('history.length');
    await js(`location.hash = 'w=f&g=999998'`);
    check('leaving it replaces the history entry', await waitFor(`${param('w')} === null && ${param('g')} === null`) && await js(`history.length === ${depth0 + 1}`), await hash());
    check('fixture: mark every flag seen', await markAll(true));
    await load();
    check('with every flag seen, walk new is off; walk all is not', JSON.stringify(await walkLabels('#walk_start button'))
        === JSON.stringify(['⚑ Walk new (0) off', `Walk all (${walk.length})`]), await walkLabels('#walk_start button'));
    await click('#walk_all');
    check('and walks them all', await waitFor(`${param('w')} === 'a' && ${param('g')} === '${gOf(0)}' && ${q('#guide_count')}.textContent === '1 / ${walk.length}'`), await bar());
    await click('#guide_exit');
    const seenList = () => js(`[...document.querySelectorAll('#summary .flag-list li')].map(li => !!li.querySelector('.flag-icon.seen .flag-mark.eye'))`);
    check('with every flag seen, each still lists, with its eye', await waitFor(`${q('#guide_bar')}.hidden`)
        && JSON.stringify(await seenList()) === JSON.stringify(R.flags.map(() => true)), await seenList());
    check('and the badges still count them, with no unseen dot', await js(`(b => !!b && !b.querySelector('.unseen-dot') && b.textContent)(${q(`li[data-node="c${c2}"] > .node .badge`)})`) === '4',
        await js(`${q(`li[data-node="c${c2}"] > .node .badge`)}?.outerHTML`));
    check('fixture: mark every flag unseen', await markAll(false));
    // A color group's walk walks that color only.
    await load();
    const yellows = walk.filter(x => R.flags.find(f => f.id === x.flag).color === 'yellow');
    await js(`${q('#summary details.flag-group.flag-yellow > summary .walk')}.click()`);
    check("a group's walk does not open the group", await js(`!${q('#summary details.flag-group.flag-yellow')}.open`));
    check("a group's walk walks its color", await waitFor(`${param('w')} === 'fy' && ${param('g')} === '${yellows[0].flag}' && ${q('#guide_count')}.textContent === '1 / ${yellows.length}'`), await bar());
    await js('history.back()');
    check("leaving the walk by the browser's Back marks its flag seen", await waitFor(`${param('w')} === null`) && await seenSoon(yellows[0].flag), await hash());
    check('fixture: mark every flag unseen again', await markAll(false));
    // The walker's own moves undo the step they leave too.
    await load(`#c${c2}=d`);
    await click('#walk_new');
    await waitFor(`${param('g')} === '${gOf(0)}' && ${shows0}`);
    const walked = new URLSearchParams((await hash()).slice(1)).get('u');
    await click('#walk_all');
    check('walk all from the same flag redoes the step with the same record', await waitFor(`${param('w')} === 'a' && ${param('u')} === ${JSON.stringify(walked)} && ${shows0}`), await hash());
    await click('#guide_exit');
    check("exit after it restores the reader's layout", await waitFor(`${param('w')} === null && ${param('u')} === null && ${param('c' + c2)} === 'd' && !${shows0}`), await hash());
    check('fixture: mark every flag unseen once more', await seenSoon(walk[0].flag) && await markAll(false));
    await load(`#g=6&c${c2}=d`);
    await click('#guide_next');
    await waitFor(`${param('g')} === '7' && !!${q(`#f${f5}.card .panel table.code-view`)}`);
    await click('#walk_new');
    check('a walk from a guide step undoes the step', await waitFor(`${param('w')} === 'f' && ${shows0} && !${q(`#f${f5}.card`)}`), await hash());
    await click('#guide_exit');
    check("and exit restores the reader's layout", await waitFor(`${param('w')} === null && ${param('c' + c2)} === 'd' && !${q(`#f${f5}.card`)} && !${shows0}`), await hash());
    check('fixture: and unseen at the end of the walker', await seenSoon(walk[0].flag) && await markAll(false));
    check('feedback shows who wrote it', await js(`[...document.querySelectorAll('#summary .notes .fb')].some(f => f.textContent.includes('why the logo') && f.querySelector('.reviewer')?.textContent === 'Ann Other <ann@example.com>')`));

    // ---- SVG: its picture on its file tab, above its source ----
    const fSvg = fileOf(C2, 'share/icon.svg');
    await load(`#at=f${fSvg}&c${c2}=f${fSvg}`);
    check('a file with no sections has no list tab and opens on its diff, the URL writing no tab',
        await waitFor(`!!${q(`#f${fSvg}.card > .panel table.diff`)}`) && await tabsOf(`#f${fSvg}`) === 'diff*|file' && await js(param('c' + c2)) === `f${fSvg}`,
        [await tabsOf(`#f${fSvg}`), await hash()]);
    check('its diff shows no picture', await js(`!${q(`#f${fSvg} .images`)}`));
    check('a file the agent has not split yet says so', await js(`${q(`#f${fSvg}`)}.textContent.includes('No sections yet') && !${q(`#f${fSvg} .sections`)}`));
    check('a file card states no split kind', await js(`![...document.querySelectorAll('#f${fSvg} dl.facts dt')].some(d => d.textContent === 'Split')`));
    // Whether its pictures have their size when its source goes in, so a
    // reveal in the box is not pushed down by them.
    await js(`(top => { window.svgSized = null; new MutationObserver((ms, o) => {
        const card = document.getElementById('f${fSvg}');
        if (!card?.querySelector(':scope > .panel table.code-view')) return;
        window.svgSized = [...card.querySelectorAll('.images img')].map(i => i.complete && i.naturalHeight > 0);
        o.disconnect();
    }).observe(top, { childList: true, subtree: true }) })(document.body)`);
    await js(`${tab(`#f${fSvg}`, 'file')}.click()`);
    await waitFor('window.svgSized !== null');
    const sized = await js('window.svgSized');
    check('its pictures are loaded before its source goes in', sized?.length === 2 && sized.every(Boolean), sized);
    check('its file tab shows before and after first, side by side, then the source', await waitFor(`(b => b && b === ${q(`#f${fSvg} > .panel`)}.firstElementChild
        && b.nextElementSibling?.matches('table.code-view')
        && [...b.querySelectorAll('figcaption')].map(x => x.textContent).join() === 'before,after'
        && [...b.querySelectorAll('img')].every(i => i.complete && i.naturalWidth > 0)
        && (([x, y]) => Math.abs(x.top - y.top) < 1 && x.right <= y.left)([...b.querySelectorAll('figure')].map(x => x.getBoundingClientRect())))(${q(`#f${fSvg} .images.preview`)})`), await hash());
    await shot('11-svg');
    await js(`${tab(`#f${fSvg}`, 'diff')}.click()`);
    check('its diff tab diffs it as text', await waitFor(`!!${q(`#f${fSvg} > .panel table.diff`)} && !${q(`#f${fSvg} .images`)}`), await hash());

    const fRename = fileOf(commit('Move the notes'), 'docs-notes.txt');
    check('renamed file card', await load(`#at=f${fRename}&c${c8}=f${fRename}`) && await waitFor(`${q(`#f${fRename}.card`)}?.textContent.includes('renamed from notes.txt')`));
    const fLicense = fileOf(commit('Move the notes'), 'LICENSE');
    const rowOf = fid => js(`(r => r && [r.querySelector('.toggle').textContent, r.querySelector('.tag')?.textContent ?? null])(${q(`#f${fid} .row`)})`);
    const treeOf = fid => js(`(n => n && [n.querySelector('.link').textContent, n.querySelector('.tag')?.textContent ?? null])(${q(`li[data-node="f${fid}"] > .node`)})`);
    check('a renamed file row shows both paths and a renamed + changed badge', JSON.stringify(await rowOf(fRename)) === JSON.stringify(['▾ notes.txt → docs-notes.txt', 'renamed + changed']), await rowOf(fRename));
    check('a file only renamed shows a renamed badge', JSON.stringify(await rowOf(fLicense)) === JSON.stringify(['▸ COPYING → LICENSE', 'renamed']), await rowOf(fLicense));
    check('and so does the tree', JSON.stringify(await treeOf(fLicense)) === JSON.stringify(['COPYING → LICENSE', 'renamed']), await treeOf(fLicense));
    await load(`#at=f${fLicense}&c${c8}=f${fLicense}`);
    check('a file only renamed has no sections and is not waiting for a split', await waitFor(`!!${q(`#f${fLicense}.card`)}`) && await js(`!${q(`#f${fLicense} .sections`)} && !${q(`#f${fLicense}`)}.textContent.includes('No sections yet')`));
    await click(`#f${fLicense} .head .widgets button[title^="Ask the agent"]`);
    check('and a request on it offers no split', await waitFor(`!!${q(`#f${fLicense} .form-slot form select`)}`) && await js(`[...${q(`#f${fLicense} .form-slot form select`)}.options].map(o => o.value).join()`) === 'debate');
    await js(`${q(`#f${fLicense} .form-slot form button[type="button"]`)}?.click()`);
    check('the commit facts count its renames', await js(`(c => c && [...c.querySelectorAll(':scope > dl.facts dt, :scope > dl.facts dd')].map(x => x.textContent).join('|'))(${q(`#f${fLicense}`)}?.closest('.card:not(#f${fLicense})'))`) === 'Renamed|2 files (1 changed)', await js(`${q(`#f${fLicense}`)}?.closest('.card:not(#f${fLicense})')?.querySelector('dl.facts')?.textContent`));
    await load(`#at=f${fRename}&c${c8}=f${fRename}`);
    check('a file the agent left whole shows no section rows and does not ask for a split', await js(`!${q(`#f${fRename} .sections`)} && !${q(`#f${fRename}`)}.textContent.includes('No sections yet')`));

    // ---- feedback a re-split moved to its file ----
    await load(`#at=f${f5}&c${c2}=f${f5}`);
    check('file feedback names the section it was on before a re-split', await waitFor(`[...document.querySelectorAll('#f${f5} .notes .fb')].some(f => f.textContent.includes('newline in shout') && f.textContent.includes('was on: const shout'))`));

    // ---- line feedback ----
    check('open file diff', await load(`#at=f${f4}&c${c2}=f${f4}~d`));
    await waitFor(`!!${q(`.panel table[data-fid="${f4}"] tr[data-new="10"]`)}`);
    check('existing line feedback under its rows', await js(`(t => t.querySelector('tr[data-new="8"] ~ tr.fb-row')?.textContent.includes('default to anon') && [...t.querySelectorAll('tr.fb-row')].some(r => r.textContent.includes('old accessor')))(${q(`.panel table[data-fid="${f4}"]`)})`));
    check('earlier-iteration feedback shows resolved_in', await js(`${q(`.panel table[data-fid="${f4}"]`)}.textContent.includes('resolved in iteration 2')`));
    await click(`.panel table[data-fid="${f4}"] tr[data-new="10"] td.ln[data-side="new"]`);
    check('line number click opens a box under the row', await waitFor(`${q(`.panel table[data-fid="${f4}"] tr[data-new="10"]`)}.nextElementSibling?.classList.contains('fb-form')`));
    await js(`${q('tr.fb-form textarea')}.value = 'Name the greeting word.'; ${q('tr.fb-form form')}.requestSubmit()`);
    check('posted line feedback shows under the row', await waitFor(`[...document.querySelectorAll('tr.fb-row')].some(r => r.textContent.includes('Name the greeting word'))`));
    let ex = await exportData();
    const lf = ex.feedback.find(f => f.body === 'Name the greeting word.');
    check('server got the line feedback', lf && lf.target === 'line' && lf.line === 10 && lf.side === 'new' && lf.path === 'lib/Foo.pm'
        && lf.commit === C2.revision && lf.section?.name === 'sub greet', lf);
    await click(`.panel table[data-fid="${f4}"] tr[data-old="8"] td.ln[data-side="old"]`);
    await js(`${q('tr.fb-form textarea')}.value = 'Old side.'; ${q('tr.fb-form form')}.requestSubmit()`);
    await waitFor(`[...document.querySelectorAll('tr.fb-row')].some(r => r.textContent.includes('Old side.'))`);
    ex = await exportData();
    check('old-side line feedback takes the section that owns the line', ex.feedback.some(f => f.body === 'Old side.' && f.side === 'old' && f.line === 8 && f.section?.name === 'sub name'), ex.feedback.at(-1));
    check('line feedback shows only at its line, under no card', await js(`![...document.querySelectorAll('.notes')].some(n => n.textContent.includes('Name the greeting word'))
        && [...document.querySelectorAll('#main .fb')].filter(f => f.textContent.includes('Name the greeting word')).length === 1`));

    // Redrawing a card keeps its open forms and what was typed in them.
    await click(`#f${f4} .head .widgets button[title="Add feedback for the agent"]`);
    await js(`${q(`#f${f4} .form-slot form textarea`)}.value = 'Half typed.'`);
    await js(`${tab(`#f${f4}`, 'list')}.click()`);
    check('an open feedback form survives its card redrawn', await waitFor(`!${q(`#f${f4} > .panel`)} && ${q(`#f${f4} .form-slot form textarea`)}?.value === 'Half typed.'`));
    await js(`${q(`#f${f4} .form-slot form button[type="button"]`)}?.click()`);
    await js(`${tab(`#f${f4}`, 'diff')}.click()`);
    await waitFor(`!!${q(`.panel table[data-fid="${f4}"] tr[data-new="10"]`)}`);
    await click(`.panel table[data-fid="${f4}"] tr[data-new="10"] td.ln[data-side="new"]`);
    await js(`${q('tr.fb-form textarea')}.value = 'Line half typed.'`);
    await click('#order_toggle button[data-value="az"]');
    // The box sits under its row, after the row's feedback.
    const boxRow = `(f => { let r = f?.previousElementSibling; while (r?.matches('.fb-row')) r = r.previousElementSibling; return f && r?.dataset.new })(${q(`.panel table[data-fid="${f4}"] tr.fb-form`)})`;
    check('a line box survives its table redrawn', await waitFor(`${q(`.panel table[data-fid="${f4}"] tr.fb-form textarea`)}?.value === 'Line half typed.' && ${boxRow} === '10'`));
    await click('#order_toggle button[data-value="reading"]');
    // Another line's box never drops what was typed in one: a box with text
    // stays open, an empty one gives way, and a line's own box takes the
    // focus again.
    const boxLines = `[...document.querySelectorAll('.panel table[data-fid="${f4}"] tr.fb-form')].map(f => f.dataset.side + f.dataset.line + '=' + f.querySelector('textarea').value).join()`;
    await waitFor(`!!${q(`.panel table[data-fid="${f4}"] tr[data-new="8"]`)}`);
    await click(`.panel table[data-fid="${f4}"] tr[data-new="8"] td.ln[data-side="new"]`);
    check('a line box with text stays open when another line is clicked', await waitFor(`${boxLines} === 'new8=,new10=Line half typed.'`), await js(boxLines));
    await click(`.panel table[data-fid="${f4}"] tr[data-old="8"] td.ln[data-side="old"]`);
    check('an empty line box gives way to the next', await waitFor(`${boxLines} === 'old8=,new10=Line half typed.' || ${boxLines} === 'new10=Line half typed.,old8='`), await js(boxLines));
    await click(`.panel table[data-fid="${f4}"] tr[data-new="10"] td.ln[data-side="new"]`);
    check("clicking a line with a box focuses that box", await waitFor(`document.activeElement?.closest('tr.fb-form')?.dataset.line === '10'`) && (await js(boxLines)).split(',').length === 2, await js(boxLines));
    await js(`for (const b of document.querySelectorAll('tr.fb-form button[type="button"]')) b.click()`);
    await shot('05-line-feedback');

    // ---- comments out of sight ----
    // A comment shows in one place; where the page stops short of it (a
    // closed row, a tab that leaves its line out), a badge counts it, and a
    // click on the badge goes to it.
    const badges = `Object.fromEntries([...document.querySelectorAll('#main .cmt-badge')].map(b => [b.parentElement.closest('[id]').id, b.textContent]))`;
    const fbCount = text => `[...document.querySelectorAll('#main .fb')].filter(f => f.textContent.includes(${JSON.stringify(text)})).length`;
    await load(`#at=s${s22}&c${c2}=f${f4},s${s22}~d`);
    check('a line comment shows once, at its line, in an open section', await waitFor(`${fbCount('Why default to anon?')} === 1 && !!${q(`#s${s22} tr.fb-row`)}`), await js(fbCount('Why default to anon?')));
    await load(`#at=c${c2}`);
    // lib/Foo.pm: four line comments and one on a section; js/util.js: one
    // on the file.
    check('a closed file row counts the comments in it', await waitFor(`(b => b.f${f4} === '5' && b.f${f5} === '1')(${badges})`), await js(badges));
    await load(`#at=f${f4}&c${c2}=f${f4}`);
    check('open on its sections, the closed section rows count theirs, the head those on no section', await waitFor(`(b => b.f${f4} === '1' && b.s${s22} === '2' && b.s${s23} === '2' && b.f${f5} === '1')(${badges})`), await js(badges));
    await click(`#s${s22} > .row .cmt-badge`);
    check('a click on a row\'s badge opens it down to the lines and shows them', await waitFor(`${param('at')} === 's${s22}' && ${fbCount('Why default to anon?')} === 1 && ${fbCount('Old side.')} === 1
        && !${q(`#s${s22} .cmt-badge`)}`), [await hash(), await js(badges)]);
    await load(`#at=f${f4}&c${c2}=f${f4}~v`);
    // The file tab leaves out the two old-side lines, and hides the section
    // comment's section.
    check('a tab that leaves comments out counts them on its head', await waitFor(`(b => b.f${f4} === '3' && Object.keys(b).length === 2)(${badges})`), await js(badges));
    // Counted again, a badge stays the same element, keeping the focus.
    await js(`${q(`#f${f4} > .head .cmt-badge`)}.focus()`);
    await js(`import(new URL('static/js/feedback.js', document.baseURI).href).then(m => m.syncCommentBadges())`);
    check('a badge counted again keeps the keyboard focus', await js(`document.activeElement === ${q(`#f${f4} > .head .cmt-badge`)} && document.activeElement.textContent === '3'`), await js('document.activeElement?.outerHTML'));
    await load(`#at=f${f4}&c${c2}=f${f4}~d`);
    await waitFor(`!!${q(`.panel table[data-fid="${f4}"] tr.fb-row`)}`);
    check('the diff tab shows every line comment; the head counts the section comment', await waitFor(`(b => b.f${f4} === '1')(${badges})`) && await js(`${fbCount('Old side.')} === 1 && ${fbCount('Name the greeting word')} === 1`), await js(badges));
    await click(`#f${f4} > .head .cmt-badge`);
    check('its badge opens the section and shows the comment', await waitFor(`${param('at')} === 's${s23}' && !!${q(`#s${s23} > .notes .fb`)}`), await hash());
    await load(`#at=f${f4}&c${c2}=f${f4}~d`);

    // ---- requests ----
    check('debate results side by side', await js(`(d => d && d.querySelector('.case.for').textContent.includes('three places') && d.querySelector('.case.against').textContent.includes('template'))(${q(`#f${f4} .notes .debate`)})`));
    await click(`#f${f4} .head .widgets button[title^="Ask the agent"]`);
    check('request form opens', await waitFor(`!!${q(`#f${f4} .form-slot form select`)}`));
    check('file request offers a split', await js(`[...${q(`#f${f4} .form-slot form select`)}.options].map(o => o.value).join()`) === 'debate,split');
    await js(`${q(`#f${f4} .form-slot form select`)}.value = 'split'; ${q(`#f${f4} .form-slot form textarea`)}.value = 'Is greet split right?'; ${q(`#f${f4} .form-slot form`)}.requestSubmit()`);
    check('posted request shows', await waitFor(`${q(`#f${f4} .notes`)}.textContent.includes('Split requested')`));
    ex = await exportData();
    check('server got the request', ex.requests.some(r => r.kind === 'split' && r.target === 'file' && r.path === 'lib/Foo.pm' && r.note === 'Is greet split right?'));

    // request from the tree on an item not open yet
    await click(`li[data-node="c${c2}"] > .node > .link`);
    await waitFor(`!!${q(`li[data-node="f${f5}"]`)}`);
    await click(`li[data-node="f${f5}"] > .node .widgets button[title^="Ask the agent"]`);
    check('tree request opens the item with its form', await waitFor(`!!${q(`#f${f5} .form-slot form select`)}`));
    await js(`${q(`#f${f5} .form-slot form`)}.requestSubmit()`);
    await waitFor(`${q(`#f${f5} .notes`)}.textContent.split('Debate requested').length > 1`);
    ex = await exportData();
    check('debate request from the tree', ex.requests.some(r => r.kind === 'debate' && r.path === 'js/util.js' && r.note === null));

    // ---- feedback widget and edit ----
    await click('#summary .widgets button[title="Add feedback for the agent"]');
    await js(`${q('#summary .form-slot form textarea')}.value = 'Overall fine.'; (f => { f.requestSubmit(); f.requestSubmit() })(${q('#summary .form-slot form')})`);
    check('review feedback posts', await waitFor(`${q('#summary .notes')}.textContent.includes('Overall fine.')`));
    ex = await exportData();
    check('a double submit posts once', ex.feedback.filter(f => f.body === 'Overall fine.').length === 1, ex.feedback.filter(f => f.body === 'Overall fine.').length);
    const fbBox = text => `[...document.querySelectorAll('#summary .notes .fb')].find(f => f.textContent.includes(${JSON.stringify(text)}))`;
    const editButton = text => `${fbBox(text)}.querySelector('button[title="Edit this feedback"]')`;
    check("no edit control on another reviewer's feedback", await js(`!${editButton('why the logo')}`));
    check('feedback with no reviewer can be edited', await js(`!!${editButton('looks unused')}`));
    await js(`${editButton('Overall fine.')}.click()`);
    await js(`(f => { f.querySelector('select').value = 'declined'; f.querySelector('textarea').value = 'Fine overall.'; f.requestSubmit() })(${q('#summary .notes .fb form')})`);
    check('edit shows the new body', await waitFor(`${q('#summary .notes')}.textContent.includes('Fine overall.')`));
    ex = await exportData();
    check('server got the edit', ex.feedback.some(f => f.status === 'declined' && f.body === 'Fine overall.' && f.reviewer === me));

    // ---- flags list navigation ----
    await load();
    await js(`[...document.querySelectorAll('#summary .flag-list button')].find(b => b.textContent.startsWith('A test showed')).click()`);
    check('flag link opens the file view at the line', await waitFor(`${param('c' + c1)} === 'f${f1}~v' && !!${q(`.panel table[data-fid="${f1}"] .flag-icon[data-flag="${flag5}"]`)}`));
    check("a flag link keeps the commit's other files listed", await js(`document.querySelectorAll('#c${c1} .files > .file').length === ${C1.files.length}`));
    check('flag line scrolled into view', await waitFor(`(r => r.top > 0 && r.bottom < innerHeight)(${q(`.panel table[data-fid="${f1}"] tr[data-new="5"]`)}.getBoundingClientRect())`));
    check("a flag's gutter icon is in its color", await waitFor(`(i => i?.classList.contains('flag-yellow') && getComputedStyle(i).color === 'rgb(229, 176, 0)')(${q(`.panel table .flag-icon[data-flag="${flag5}"]`)})`));

    // ---- the flag jumped to ----
    await load();
    await flagLink('A test showed');
    const f1row = `.panel table[data-fid="${f1}"] tr[data-new="5"]`;
    await waitFor(`!!${q(f1row + '[data-flag-focus]')}`);
    check('a line flag link marks its line in its color, its sentence above, in view below the stuck heads',
        await focused([f1row], 'flag-yellow', 'A test showed the constructor must copy the arguments.', aboveRow(f1row)) === true,
        await focused([f1row], 'flag-yellow', 'A test showed', aboveRow(f1row)));
    check('on its file tab', await tabsOf(`#f${f1}`) === 'list|diff|file*', await tabsOf(`#f${f1}`));
    check("its bar is thicker than a guide highlight's", await js(`getComputedStyle(${q(f1row + ' > td.ln')}).boxShadow.includes('10px')`));
    check('the pulse runs', await js(`getComputedStyle(${q('.flag-callout')}).animationName`) === 'flag-arrive');
    await shot('20-flag-focus-line');
    await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: 'reduce' }] });
    check('and stays still for a reader who asks for reduced motion', await js(`getComputedStyle(${q('.flag-callout')}).animationName`) === 'none');
    await send('Emulation.setEmulatedMedia', { features: [] });
    await flagLink('The README was not');
    const readmeLine = cardFlag(`#f${f8}`, flagOf('The README was not'));
    await waitFor(`!!${q(readmeLine + '[data-flag-focus]')}`);
    check('a whole-file flag link marks its line on the file card',
        await focusedLine(readmeLine, 'flag-red', 'The README was not in the plan.') === true, await focusedLine(readmeLine, 'flag-red', 'The README'));
    check('its line pulses keeping its bar', await js(`(s => s.animationName === 'flag-arrive-line' && s.boxShadow.includes('inset'))(getComputedStyle(${q(readmeLine)}))`),
        await js(`(s => [s.animationName, s.boxShadow])(getComputedStyle(${q(readmeLine)}))`));
    await flagLink('The binary file');
    const c1line = cardFlag(`#c${c1} > .card`, flagOf('The binary file'));
    await waitFor(`!!${q(c1line + '[data-flag-focus]')}`);
    check('a commit flag link marks its line on the commit card',
        await focusedLine(c1line, 'flag-yellow', 'The binary file') === true, await focusedLine(c1line, 'flag-yellow', 'The binary file'));
    check('following a flag link marks the flag seen', await waitFor(`!!${q(c1line + ' > .flag-icon .flag-mark.eye')}`));
    await flagLink('Bar copies');
    const blobRows = [3, 4].map(n => `#b${c9} .panel table tr[data-new="${n}"]`);
    await waitFor(`!!${q(blobRows[0] + '[data-flag-focus]')}`);
    check('a flag on a file its commit did not change marks the lines of the whole-file card',
        await focused(blobRows, 'flag-yellow', 'Bar copies the constructor of Foo.', aboveRow(blobRows[0])) === true,
        await focused(blobRows, 'flag-yellow', 'Bar copies', aboveRow(blobRows[0])));
    // A deleted file's flag marks its old lines, tinted over their removed
    // color, side by side its old cells only.
    const todoRows = [2, 3].map(n => `.panel table[data-fid="${fTodo}"] tr[data-old="${n}"]`);
    const look = sel => js(`(s => [s.backgroundImage, s.backgroundColor, s.boxShadow])(getComputedStyle(${q(sel)}))`);
    const yellowTint = x => x[0].includes('gradient');
    await flagLink('The TODO list');
    await waitFor(`!!${q(todoRows[0] + '[data-flag-focus]')}`);
    check("a deleted file's line flag marks its old lines in the diff",
        await focused(todoRows, 'flag-yellow', 'The TODO list goes', aboveRow(todoRows[0])) === true, await focused(todoRows, 'flag-yellow', 'The TODO list goes', aboveRow(todoRows[0])));
    check('on its diff, a deleted file with no sections having no other tab, so no tabs',
        await js(`!${q(`#f${fTodo} > .head .tabs`)} && !!${q(`#f${fTodo} > .panel table.diff`)}`), await hash());
    let seen = await look(todoRows[0] + ' > td.code');
    check('its tint shows over the removed color', yellowTint(seen) && seen[1] === 'rgb(255, 224, 224)', seen);
    // The guide's highlight on the same line does not win over it.
    await js(`(r => { r.classList.add('hl'); r.querySelector('td.ln').classList.add('hl'); r.querySelector('td.ln').dataset.flagged = 'flag-yellow' })(${q(todoRows[0])})`);
    seen = [await look(todoRows[0] + ' > td.code'), await look(todoRows[0] + ' > td.ln')];
    check("and over a guide highlight, its bar replacing the guide's", yellowTint(seen[0]) && seen[1][2].includes('10px') && !seen[1][2].includes('6px'), seen);
    await click('#diff_toggle button[data-value="split"]');
    await flagLink('The TODO list');
    const todoCells = todoRows.flatMap(r => [1, 2].map(n => `${r} > td:nth-child(${n})`));
    await waitFor(`!!${q(`.panel table.split[data-fid="${fTodo}"] [data-flag-focus]`)}`);
    check('side by side it marks the old cells of those lines',
        await focused(todoCells, 'flag-yellow', 'The TODO list goes', aboveRow(todoRows[0])) === true, await focused(todoCells, 'flag-yellow', 'The TODO list goes', aboveRow(todoRows[0])));
    seen = [await look(todoCells[1]), await look(todoCells[0])];
    check('tinted over the removed color, the bar on the line number', yellowTint(seen[0]) && seen[0][1] === 'rgb(255, 224, 224)' && seen[1][2].includes('10px'), seen);
    await click('#diff_toggle button[data-value="unified"]');
    // An added line takes the tint too, over its added color.
    await load(`#c${c2}=f${f4}~d`);
    const addRow = `.panel table[data-fid="${f4}"] tr.add`;
    await waitFor(`!!${q(addRow)}`);
    await js(`(r => { r.dataset.flagFocus = 'flag-red'; r.classList.add('hl') })(${q(addRow)})`);
    seen = await look(addRow + ' > td.code');
    check('an added line takes the tint over its added color and a guide highlight', seen[0].includes('gradient') && !seen[0].includes('255, 170, 0') && seen[1] === 'rgb(224, 255, 224)', seen);
    // A line flag on a file with no tab for it marks its line on the card.
    await load();
    await flagLink('The old blob');
    const binLine = cardFlag(`#f${fOldBin}`, flagOf('The old blob'));
    await waitFor(`!!${q(binLine + '[data-flag-focus]')}`);
    check('a line flag on a binary file marks its line on the card, no tab opened',
        await focusedLine(binLine, 'flag-yellow', 'The old blob goes') === true && await js(`!${q(`#f${fOldBin} > .panel`)}`), await focusedLine(binLine, 'flag-yellow', 'The old blob goes'));
    // A line flag whose panel has no line table (an image's file tab) marks
    // its line on the card once the panel settles.
    await load();
    await flagLink('The logo picture');
    const fLogoImg = fileOf(C1, 'share/logo.png');
    const imgLine = cardFlag(`#f${fLogoImg}`, flagOf('The logo picture'));
    await waitFor(`!!${q(imgLine + '[data-flag-focus]')}`);
    check("a line flag on an image's file tab marks its line on the card once the panel settles",
        await js(`!!${q(`#f${fLogoImg} > .panel img`)} && !${q(`#f${fLogoImg} > .panel .loading`)}`)
        && await focusedLine(imgLine, 'flag-yellow', 'The logo picture') === true, await focusedLine(imgLine, 'flag-yellow', 'The logo picture'));
    await js(`${q('#tree li[data-node="summary"] .link')}.click()`);
    check('navigating elsewhere drops the mark', await waitFor(`${param('at')} === 'summary'`) && await waitFor(unfocusedExpr));

    // ---- flags on the card ----
    // A card shows the flags no row under it carries, whatever tab is open,
    // so every flag its badge counts can be found on it.
    // Each line's text, less its read-aloud buttons.
    const cardFlagText = card => js(`[...document.querySelectorAll(${JSON.stringify(card + ' > .flag-slot > ul.card-flags > li')})].map(li => (c => (c.querySelector('.speak')?.remove(), c.textContent.trim()))(li.cloneNode(true)))`);
    const popupOf = async (card, id) => {
        await click(cardFlag(card, id) + ' > [data-flag]');
        return waitFor(`(p => p && p.textContent.includes(${JSON.stringify(R.flags.find(f => f.id === id).sentence)}))(${q('.flag-popup')})`);
    };
    // The open flags a file's badge counts, and those its section rows' badges
    // and its card's open flag lines show.
    const reachable = fid => js(`(c => [
        Number(c.querySelector(':scope > .head .row .badge')?.textContent ?? 0),
        [...c.querySelectorAll(':scope > .sections > .section .row .badge')].reduce((n, b) => n + Number(b.textContent), 0)
            + c.querySelectorAll(':scope > .flag-slot > ul.card-flags > li > .flag-icon').length,
    ])(${q(`#f${fid}`)})`);
    const fAssumed = flagOf('Assumed nothing');
    await load(`#at=f${f4}&c${c2}=f${f4}`);
    await waitFor(`!!${q(`#f${f4} > .sections`)}`);
    check('a whole-file flag, and a line flag in none of its listed sections with its lines, show on the file card: icon, kind and sentence',
        JSON.stringify(await cardFlagText(`#f${f4}`)) === JSON.stringify(['⚑ assumption: Assumed nothing else reads the name field directly.',
            '⚑ other: The constructor is unchanged; callers still pass a name. :5']), await cardFlagText(`#f${f4}`));
    check('clicking one opens its popup', await popupOf(`#f${f4}`, fAssumed));
    await js(`document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))`);
    const [badge, found] = await reachable(f4);
    check("the file's badge counts what its section rows and card show", badge === 3 && found === badge, [badge, found]);
    // The popup just opened marked the whole-file flag seen; it stays on the
    // card, with its eye, and counted.
    check('a flag just seen stays on its card, with its eye, and counted', await waitFor(`!!${q(cardFlag(`#f${f4}`, fAssumed) + ' .flag-icon.seen .flag-mark.eye')}
        && ${q(`#f${f4} > .head .row .badge`)}?.textContent === '3'`), await reachable(f4));
    await js(`${tab(`#f${f4}`, 'diff')}.click()`);
    check('the lines show on its diff tab too', await waitFor(`!!${q(`#f${f4} > .panel table.diff`)} && !!${q(cardFlag(`#f${f4}`, fAssumed))}`));
    await click(cardFlag(`#f${f4}`, flagOf('The constructor is unchanged')) + ' > .where');
    const newRow = `.panel table[data-fid="${f4}"] tr[data-new="5"]`;
    check("a line flag's lines jump to them, marked, on the file tab", await waitFor(`${param('c' + c2)} === 'f${f4}~v' && !!${q(newRow + '[data-flag-focus]')}`)
        && await focused([newRow], 'flag-yellow', 'The constructor is unchanged', aboveRow(newRow)) === true, await hash());
    // The keyboard: Space opens a gutter flag's popup and the focus moves
    // into it; using a control there, which draws the popup and every icon
    // again, keeps the focus in it; Escape brings the focus back to the icon
    // that opened it, not the same flag's icon on its card above.
    const ctorIcon = `${newRow} .flag-icon[data-flag="${flagOf('The constructor is unchanged')}"]`;
    await js(`${q(ctorIcon)}.focus()`);
    await js(`document.activeElement.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', bubbles: true }))`);
    check("Space opens a gutter flag's popup, the focus in it", await waitFor(`!!${q('.flag-popup')} && ${q('.flag-popup')}.contains(document.activeElement)`));
    await js(`(b => { b.focus(); b.click() })([...${q('.flag-popup')}.querySelectorAll('button')].find(b => /^Mark (un)?seen$/.test(b.textContent)))`);
    check('using its seen toggle keeps the focus on it', await waitFor(`${q('.flag-popup')}?.contains(document.activeElement) && /^Mark (un)?seen$/.test(document.activeElement.textContent)
        && !${q(cardFlag(`#f${f4}`, flagOf('The constructor is unchanged')) + ' .flag-icon .flag-mark.eye')}`));
    await js(`document.activeElement.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))`);
    check('Escape closes it, the focus back on the icon that opened it', await waitFor(`!${q('.flag-popup')} && document.activeElement?.matches('.flag-icon[data-flag]') && document.activeElement.closest('tr') === ${q(newRow)}`),
        await js(`document.activeElement?.outerHTML.slice(0, 120)`));
    await load(`#at=c${c1}`);
    check('a whole-commit flag shows on its commit card',
        await waitFor(`!!${q(cardFlag(`#c${c1} > .card`, flagOf('The binary file')))}`)
        && JSON.stringify(await cardFlagText(`#c${c1} > .card`)) === JSON.stringify(['⚑ self-review: The binary file was added by mistake and is removed in the next commit.']),
        await cardFlagText(`#c${c1} > .card`));
    check('its popup gives the reason its color changed', await popupOf(`#c${c1} > .card`, flagOf('The binary file'))
        && await js(`${q('.flag-popup .flag-reason')}?.textContent`) === 'Color changed: Removed in the next commit, as planned.' && await js(`${q('.flag-popup')}.textContent.includes('Yellow flag, a reviewer may object')`));
    await js(`document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))`);
    await load(`#at=c${c9}&c${c9}=d`);
    check("a flag on a file its commit did not change shows on the commit's card with its path and lines, on its diff tab too",
        await waitFor(`!!${q(`#c${c9} > .card > .panel table.diff`)}`)
        && JSON.stringify(await cardFlagText(`#c${c9} > .card`)) === JSON.stringify(['⚑ note: Bar copies the constructor of Foo. lib/Foo.pm:3–4']), await cardFlagText(`#c${c9} > .card`));
    await click(cardFlag(`#c${c9} > .card`, flagOf('Bar copies')) + ' > [data-flag]');
    check("a pending flag's popup says the agent has yet to decide on it", await waitFor(`${q('.flag-popup')}?.textContent.includes('the agent has yet to keep, recolor or retire it')`));
    await js(`document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))`);

    // ---- panels open in place ----
    await load(`#at=s${s22}&c${c2}=f${f4},s${s22}`);
    for (const [label, sel] of [['diff', `#s${s22}`], ['file', `#s${s22}`], ['diff', `#f${f5}`], ['diff', `#f${f4}`]]) {
        if (sel === `#f${f5}`) await load(`#at=f${f5}&c${c2}=f${f5}`);
        // An open diff above the file does not move it, once it has filled.
        if (sel === `#f${f4}`) {
            await load(`#at=f${f4}&c${c2}=f${f4},f${f5}~d`);
            await waitFor(`!!${q(`#f${f5} > .panel table.diff`)}`);
        }
        await js(`window.scrollTo(0, ${q(sel)}.getBoundingClientRect().top + scrollY - 300)`);
        // A section's panels are widgets; a file's are tabs.
        const button = (sel[1] === 'f' ? tab : tool)(sel, label);
        const before = await js(`${button}.getBoundingClientRect().top`);
        await js(`${button}.click()`);
        const opened = await waitFor(`!!${q(sel + (label === 'diff' ? ' .panel table.diff' : ' .panel table.code-view'))}`);
        const after = await js(`${button}.getBoundingClientRect().top`);
        check(`${label} on ${sel} opens in place`, opened && Math.abs(after - before) < 1, { opened, before, after });
    }
    // A tab clicked while a diff above is still filling from an earlier
    // apply holds once that fill lands too.
    await load(`#at=f${f4}&c${c2}=f${f4}`);
    await slowDiff(f5);
    await js(`location.hash = '#at=f${f4}&c${c2}=f${f4},f${f5}~d'`);
    const loading = await waitFor(`!!${q(`#f${f5} > .panel .loading`)}`);
    await js(`window.scrollTo(0, ${q(`#f${f4}`)}.getBoundingClientRect().top + scrollY - 300)`);
    const heldTab = tab(`#f${f4}`, 'diff');
    const heldAt = await js(`${heldTab}.getBoundingClientRect().top`);
    await js(`${heldTab}.click()`);
    const unanchored = await js(`getComputedStyle(${q('#main')}).overflowAnchor === 'none'`);
    const filled = await waitFor(`!!${q(`#f${f5} > .panel table.diff`)} && !!${q(`#f${f4} > .panel table.diff`)}`);
    const heldOk = await waitFor(`Math.abs(${heldTab}.getBoundingClientRect().top - ${heldAt}) < 1`, 1000);
    check('a tab clicked under a diff still filling holds once the fill lands', loading && filled && heldOk,
        { loading, filled, heldAt, now: await js(`${heldTab}.getBoundingClientRect().top`) });
    // The browser does not anchor the scroll while the page waits to place
    // itself, so a scroll then is the reader's; it does again after.
    check('no scroll anchoring while the page waits on a fill, as before once it lands',
        unanchored && await waitFor(`getComputedStyle(${q('#main')}).overflowAnchor === 'auto'`), unanchored);

    // ---- a header clicked holds still under the mouse ----
    // A commit's, file's or section's header stays where it was, under the
    // mouse, after a real click on its tabs or widgets redraws what is under
    // it, from tall to short and back, also stuck over its card scrolled
    // above (then its card's content starts under it again), and when a fill
    // above lands later.
    const headOf = id => `(n => (n.matches('.card') ? n : n.querySelector(':scope > .card')).querySelector(':scope > .head'))(document.getElementById('${id}'))`;
    // A real mouse click in the middle of what expr names.
    const mouseClick = async expr => {
        const [x, y] = await js(`(r => [Math.round(r.left + r.width / 2), Math.round(r.top + r.height / 2)])((${expr}).getBoundingClientRect())`);
        for (const type of ['mousePressed', 'mouseReleased']) await send('Input.dispatchMouseEvent', { type, x, y, button: 'left', clickCount: 1 });
    };
    // The form open right under id's header.
    const formUnder = id => `(h => h.nextElementSibling?.matches('.form-slot') && h.nextElementSibling.querySelector(':scope > form'))(${headOf(id)})`;
    // Scroll id's header to the middle of the screen, or, stuck, its card's
    // top up to 150px above where its header sticks; whether it is stuck.
    const placeHead = (id, stuck) => js(`(h => {
        const card = h.parentElement;
        const at = parseFloat(getComputedStyle(h).top);
        const by = card.getBoundingClientRect().height - h.offsetHeight - 40;
        window.scrollBy(0, ${stuck} ? card.getBoundingClientRect().top - at + by : h.getBoundingClientRect().top - innerHeight * 0.4);
        return h.getBoundingClientRect().top - card.getBoundingClientRect().top > 20;
    })(${headOf(id)})`);
    const settled = () => waitFor(`!document.querySelector('#main .loading')`, 5000).then(() => sleep(200));
    const clickHeld = async (id, label) => {
        const button = `[...${headOf(id)}.querySelectorAll('.tabs button, .widgets button')].find(b => b.textContent === '${label}')`;
        const [x, y, top, was] = await js(`(r => [Math.round(r.left + r.width / 2), Math.round(r.top + r.height / 2), ${headOf(id)}.getBoundingClientRect().top, location.hash])(${button}.getBoundingClientRect())`);
        for (const type of ['mousePressed', 'mouseReleased']) await send('Input.dispatchMouseEvent', { type, x, y, button: 'left', clickCount: 1 });
        const went = await waitFor(`location.hash !== ${JSON.stringify(was)}`);
        await settled();
        const [now, under] = await js(`[${headOf(id)}.getBoundingClientRect().top, document.elementFromPoint(${x}, ${y})?.closest('.head') === ${headOf(id)}]`);
        return { label, top, now, under, ok: went && under && Math.abs(now - top) < 1 };
    };
    const heldBase = `#c${c1}=d&c${c2}=f${f4},s${s22}~d,s${s23}~d,f${f5}~d&c${c3}=d`;
    for (const [id, labels] of [[`s${s22}`, ['file', 'diff', 'file', 'diff']], [`f${f4}`, ['diff', 'file', 'list', 'file', 'diff', 'list']],
        [`c${c2}`, ['diff', 'list', 'diff', 'list']]]) {
        for (const stuck of [false, true]) {
            await load(heldBase);
            await settled();
            const moves = [];
            let stuckOnce = false;
            for (const label of labels) {
                const isStuck = await placeHead(id, stuck);
                stuckOnce ||= isStuck;
                const r = await clickHeld(id, label);
                moves.push({ ...r, isStuck });
            }
            check(`${id}'s header holds under the mouse${stuck ? ', stuck' : ''}, switching ${labels.join(', ')}`,
                moves.every(m => m.ok) && (!stuck || stuckOnce), moves.filter(m => !m.ok || stuck));
        }
    }
    // A fill above landing after the click keeps it there too.
    for (const [id, label] of [[`s${s22}`, 'file'], [`f${f4}`, 'diff']]) {
        for (const stuck of [false, true]) {
            await load(`#c${c2}=f${f4},s${s22}${stuck ? '~v' : '~d'},f${f5}~d`);
            await settled();
            await slowDiff(f1);
            // As in a browser that never anchors the scroll: the page places
            // the header itself once the fill lands.
            await js(`${q('#main')}.style.overflowAnchor = 'none'`);
            await js(`location.hash = '#c${c1}=d&c${c2}=f${f4},s${s22}${stuck ? '~v' : '~d'},f${f5}~d'`);
            const loadingAbove = await waitFor(`!!${q(`#c${c1} .panel .loading`)}`);
            const isStuck = await placeHead(id, stuck);
            const r = await clickHeld(id, stuck && id[0] === 's' ? 'diff' : label);
            check(`${id}'s header holds under the mouse${stuck ? ', stuck' : ''} when a diff above fills after the click`,
                loadingAbove && r.ok && isStuck === stuck && await js(`!!${q(`#c${c1} .panel table.diff`)}`), { loadingAbove, isStuck, ...r });
        }
    }

    // A form opened on a stuck header while an earlier click still waits on
    // a fill above holds that header once the fill lands: the page's own
    // scroll for it is not the reader's.
    {
        await load(`#c${c2}=f${f4},s${s22}~v,f${f5}~d`);
        await settled();
        await slowDiff(f1);
        await js(`${q('#main')}.style.overflowAnchor = 'none'`);
        await js(`location.hash = '#c${c1}=d&c${c2}=f${f4},s${s22}~v,f${f5}~d'`);
        const loadingAbove = await waitFor(`!!${q(`#c${c1} .panel .loading`)}`);
        await js(`(h => window.scrollBy(0, h.parentElement.getBoundingClientRect().top - parseFloat(getComputedStyle(h).top) + 100))(${headOf(`f${f4}`)})`);
        await mouseClick(`[...${headOf(`s${s22}`)}.querySelectorAll('.widgets button')].find(b => b.textContent === 'diff')`);
        await waitFor(`!!${q(`#s${s22} .panel table.diff, #s${s22} .panel .loading`)}`);
        const stuckF = await js(`(h => h.getBoundingClientRect().top - h.parentElement.getBoundingClientRect().top > 20)(${headOf(`f${f4}`)})`);
        await mouseClick(`[...${headOf(`f${f4}`)}.querySelectorAll('.widgets button')].find(b => b.textContent === 'feedback')`);
        const formShown = await waitFor(`!!${formUnder(`f${f4}`)}`);
        const fTop = await js(`${headOf(`f${f4}`)}.getBoundingClientRect().top`);
        const stillLoading = await js(`!!${q(`#c${c1} .panel .loading`)}`);
        await settled();
        const fNow = await js(`${headOf(`f${f4}`)}.getBoundingClientRect().top`);
        check('a form opened on a stuck header while a fill above still waits keeps that header once the fill lands',
            loadingAbove && stuckF && formShown && stillLoading && Math.abs(fNow - fTop) < 1 && await js(`!!${q(`#c${c1} .panel table.diff`)}`),
            { loadingAbove, stuckF, formShown, stillLoading, fTop, fNow });
    }

    // So does one opened while a flag link still waits on a fill above: the
    // page does not then go on to the flag.
    {
        await load();
        await settled();
        await slowDiff(f1);
        await js(`${q('#main')}.style.overflowAnchor = 'none'`);
        await js(`location.hash = '#c${c1}=d'`);
        const loadingAbove = await waitFor(`!!${q(`#c${c1} .panel .loading`)}`);
        await flagLink('The README was not');
        const drawn = await waitFor(`!!${q(`#f${f8}`)}`);
        await mouseClick(`[...${headOf(`c${c4}`)}.querySelectorAll('.widgets button')].find(b => b.textContent === 'feedback')`);
        const formShown = await waitFor(`!!${formUnder(`c${c4}`)}`);
        // Its card's top, which moves even when the header sticks.
        const cardTop = `${headOf(`c${c4}`)}.parentElement.getBoundingClientRect().top`;
        const cTop = await js(cardTop);
        const stillLoading = await js(`!!${q(`#c${c1} .panel .loading`)}`);
        await settled();
        const cNow = await js(cardTop);
        check('a form opened while a flag link still waits on a fill above keeps its header, the page not going on to the flag',
            loadingAbove && drawn && formShown && stillLoading && Math.abs(cNow - cTop) < 1 && await js(`!!${q(`#c${c1} .panel table.diff`)}`),
            { loadingAbove, drawn, formShown, stillLoading, cTop, cNow });
    }

    // Closing a merged-in commit or a whole-file view from its header puts
    // what takes its place where that header was: the commit's row, or the
    // commit's card.
    for (const [name, hash, head, after] of [
        ['a merged-in commit', `#c${c7}=c${c4},c${c6}`, headOf(`c${c6}`), q(`#c${c6}.level:not(.card)`)],
        ['the last merged-in commit open', `#c${c7}=c${c6}`, headOf(`c${c6}`), q(`#c${c6}.level:not(.card)`)],
        ['a whole-file view', `#c${c8}=p${encodeURIComponent('lib/Foo.pm')}`, q(`#b${c8} > .head`), headOf(`c${c8}`)],
    ]) {
        await load(hash);
        await settled();
        await js(`window.scrollBy(0, ${head}.getBoundingClientRect().top - innerHeight * 0.4)`);
        const [top, was] = await js(`[${head}.getBoundingClientRect().top, location.hash]`);
        await mouseClick(`${head}.querySelector('.toggle') ?? ${head}`);
        const went = await waitFor(`location.hash !== ${JSON.stringify(was)}`);
        await settled();
        const now = await js(`${after}?.getBoundingClientRect().top`);
        check(`closing ${name} from its header puts what takes its place where the header was`, went && Math.abs(now - top) < 1, { went, top, now });
    }
    // Near the end of the page, room grows under it so the header holds;
    // it goes once the reader scrolls back up.
    const fLast = fileOf(commit('Add Bar, a counter'), 'lib/Bar.pm');
    await load(`#c${c9}=f${fLast}~v`);
    await settled();
    await js('window.scrollTo(0, document.documentElement.scrollHeight)');
    const [endStuck, endAt] = await js(`(h => [h.getBoundingClientRect().top - h.parentElement.getBoundingClientRect().top > 20, h.getBoundingClientRect().top])(${headOf(`f${fLast}`)})`);
    await mouseClick(`${headOf(`f${fLast}`)}.querySelector('.toggle')`);
    const endRow = await waitFor(`!${q(`#f${fLast}.card`)} && Math.abs(${q(`#f${fLast}`)}.getBoundingClientRect().top - ${endAt}) < 1`);
    check('collapsing the last file from its stuck head at the end of the page leaves its row where the head was', endStuck && endRow,
        { endStuck, endAt, top: await js(`${q(`#f${fLast}`)}.getBoundingClientRect().top`) });
    // Expanding it again from its row there gives back the room it needs no
    // longer, with no scroll.
    const roomWas = await js(`${q('#room')}.offsetHeight`);
    await mouseClick(`${q(`#f${fLast} .toggle`)}`);
    const reRow = await waitFor(`!!${q(`#f${fLast}.card`)} && Math.abs(${headOf(`f${fLast}`)}.getBoundingClientRect().top - ${endAt}) < 1`);
    await settled();
    const roomNow = await js(`${q('#room')}.offsetHeight`);
    check('expanding it again from its row gives back room it no longer needs', reRow && roomWas > 0 && roomNow < roomWas
        && await js(`${q('#room')}.getBoundingClientRect().top >= innerHeight - 1 || ${q('#room')}.offsetHeight === 0`), { reRow, roomWas, roomNow });
    await js('window.scrollTo(0, 0)');
    check('the room under the page goes once the reader scrolls up', await waitFor(`${q('#room')}.offsetHeight === 0`), await js(`${q('#room')}.offsetHeight`));
    // A header its card's end pushes up from where it sticks holds where it
    // sticks, its card's content under it, once the card grows.
    await load(`#c${c2}=f${f4}&c${c3}=d`);
    await settled();
    const [pushed, sticksAt] = await js(`(h => {
        const at = parseFloat(getComputedStyle(h).top);
        window.scrollBy(0, h.parentElement.getBoundingClientRect().bottom - at - h.offsetHeight / 2);
        return [h.getBoundingClientRect().top < at - 2, at];
    })(${headOf(`f${f4}`)})`);
    await js(`${tab(`#f${f4}`, 'diff')}.click()`);
    await settled();
    const [pHead, pCard] = await js(`(h => [h.getBoundingClientRect().top, h.parentElement.getBoundingClientRect().top])(${headOf(`f${f4}`)})`);
    check('a header pushed up from where it sticks holds where it sticks, its content under it, once its card grows',
        pushed && Math.abs(pHead - sticksAt) < 1 && Math.abs(pCard - sticksAt) < 1, { pushed, sticksAt, pHead, pCard });
    // Left a short card instead (here only its head shows once its list
    // tab redraws it), it stays where it was, short of the card's end.
    await load(`#c${c2}=f${f4}~d&c${c3}=d`);
    await settled();
    await js(`document.head.append(Object.assign(document.createElement('style'), { id: 'short_card',
        textContent: '#f${f4} > :not(.head):not(:has(table.diff)) { display: none }' }))`);
    const [shortPushed, shortAt] = await js(`(h => {
        const at = parseFloat(getComputedStyle(h).top);
        window.scrollBy(0, h.parentElement.getBoundingClientRect().bottom - at - h.offsetHeight + 10);
        return [h.getBoundingClientRect().top < at - 2, h.getBoundingClientRect().top];
    })(${headOf(`f${f4}`)})`);
    const [sx, sy] = await js(`(r => [Math.round(r.left + r.width / 2), Math.ceil(Math.max(r.top + r.height / 2, parseFloat(getComputedStyle(${headOf(`f${f4}`)}).top) + 3))])(${tab(`#f${f4}`, 'list')}.getBoundingClientRect())`);
    for (const type of ['mousePressed', 'mouseReleased']) await send('Input.dispatchMouseEvent', { type, x: sx, y: sy, button: 'left', clickCount: 1 });
    const shortened = await waitFor(`!${q(`#f${f4} > .panel table.diff`)}`);
    await settled();
    const [shortNow, shortCard] = await js(`(h => [h.getBoundingClientRect().top, h.parentElement.offsetHeight - h.offsetHeight])(${headOf(`f${f4}`)})`);
    await js(`${q('#short_card')}.remove()`);
    check('a header pushed up from where it sticks, its card left short, stays where it was', shortPushed && shortened && Math.abs(shortNow - shortAt) < 1,
        { shortPushed, shortened, shortAt, shortNow, shortCard });
    // Collapsed instead, it leaves its row where the head was (pushed up a
    // little, so the mouse reaches it under the heads stuck above).
    await load(`#c${c2}=f${f4}&c${c3}=d`);
    await settled();
    const [pushedC, pushedAt] = await js(`(h => {
        const at = parseFloat(getComputedStyle(h).top);
        window.scrollBy(0, h.parentElement.getBoundingClientRect().bottom - at - h.offsetHeight + 10);
        return [h.getBoundingClientRect().top < at - 2, h.getBoundingClientRect().top];
    })(${headOf(`f${f4}`)})`);
    const [px, py] = await js(`(r => [Math.round(r.left + r.width / 2), Math.ceil(Math.max(r.top + r.height / 2, parseFloat(getComputedStyle(${headOf(`f${f4}`)}).top) + 3))])(${headOf(`f${f4}`)}.querySelector('.toggle').getBoundingClientRect())`);
    for (const type of ['mousePressed', 'mouseReleased']) await send('Input.dispatchMouseEvent', { type, x: px, y: py, button: 'left', clickCount: 1 });
    await settled();
    const pRow = await waitFor(`!${q(`#f${f4}.card`)} && Math.abs(${q(`#f${f4}`)}.getBoundingClientRect().top - ${pushedAt}) < 1`);
    check('a header pushed up from where it sticks, collapsed, leaves its row where the head was', pushedC && pRow,
        { pushedC, pushedAt, top: await js(`${q(`#f${f4}`)}.getBoundingClientRect().top`) });

    // ---- files expand in place ----
    const rowsOf = cid => js(`[...document.querySelectorAll('#c${cid} .files > .file')].map(f => [f.id, f.classList.contains('card'), f.querySelector('.toggle').getAttribute('aria-expanded')])`);
    await load(`#at=f${f4}&c${c2}=f${f4}`);
    let fileRows = await rowsOf(c2);
    check('an open commit lists every file, the opened one expanded', fileRows.length === C2.files.length && fileRows.filter(r => r[1]).map(r => r[0]).join() === `f${f4}`
        && fileRows.every(r => r[2] === String(r[1])), fileRows);
    check("the commit's own summary stays above its files", await js(`(c => c.querySelector(':scope > .head h2')?.textContent === '${C2.position}. ${C2.subject}'
        && [...c.querySelectorAll(':scope > .prose-head > h3')].map(h => h.textContent).slice(0, 3).join() === 'What,Why,How')(${q(`#c${c2} > .card`)})`));
    check('a collapsed file row shows only path, counts and flags, its status in the tooltip', await js(`(r => r.querySelector('.toggle').title === 'js/util.js (added)'
        && !!r.querySelector('.counts') && !r.querySelector('.meta, .gist') && !r.textContent.includes('added'))(${q(`#f${f5} > .row`)})`), await js(`${q(`#f${f5} > .row`)}.outerHTML`));
    const corners = sel => js(`(s => [s.borderTopLeftRadius, s.borderTopRightRadius, s.borderBottomRightRadius, s.borderBottomLeftRadius, s.borderBottomWidth])(getComputedStyle(${q(sel)}))`);
    const rowCorners = await corners(`#f${f5}`);
    check('a collapsed file row is outlined with every corner rounded', rowCorners.slice(0, 4).every(r => parseFloat(r) > 0) && rowCorners[4] === '1px', rowCorners);
    const cardCorners = await corners(`#f${f4}`);
    check('an expanded file rounds only its top-left corner, with no outline', parseFloat(cardCorners[0]) > 0 && cardCorners.slice(1, 4).every(r => parseFloat(r) === 0) && cardCorners[4] === '0px', cardCorners);
    await click(`#f${f5} > .row`);
    check('a second file expands beside the first', await waitFor(`!!${q(`#f${f5}.card`)} && !!${q(`#f${f4}.card`)} && ${param('c' + c2)} === '${[f4, f5].sort((a, b) => a - b).map(f => 'f' + f).join()}'`), await hash());
    await shot('10a-files-expanded');
    await js(`${tab(`#f${f5}`, 'diff')}.click()`);
    check("a head's tabs do not collapse it", await waitFor(`!!${q(`#f${f5}.card .panel table.diff`)}`), await hash());
    await click(`#f${f5} > .head .widgets button[title="Add feedback for the agent"]`);
    check('nor does its feedback button', await waitFor(`!!${q(`#f${f5}.card .form-slot form`)}`));
    await js(`${q(`#f${f5} .form-slot form button[type="button"]`)}?.click()`);
    await click(`#f${f4} > .head`);
    check("clicking a file's head collapses only that file", await waitFor(`!${q(`#f${f4}.card`)} && !!${q(`#f${f4} > .row .toggle`)} && !!${q(`#f${f5}.card`)}`), await hash());
    check("a collapsed row shows no summary text", await js(`!!${q(`#f${f4} > .row`)} && !${q(`#f${f4} > .row .gist`)} && !${q(`#f${f4} > .row`)}.textContent.includes('Defaults the name')`));
    check('no close buttons on files', await js(`!document.querySelector('#commits .tool.close')`));
    const oneOpen = await hash();
    check('reload restores which files are expanded', await load(oneOpen) && await waitFor(`!!${q(`#f${f5}.card .panel table.diff`)} && !${q(`#f${f4}.card`)} && !!${q(`#f${f4} .toggle`)}`), oneOpen);
    await click(`#f${f5} .toggle`);
    check("the expanded file's toggle collapses it", await waitFor(`!${q(`#f${f5}.card`)} && ${param('c' + c2)} === null`), await hash());
    await js('history.back()');
    check('back expands it again', await waitFor(`!!${q(`#f${f5}.card`)}`), await hash());
    await js(`(t => (t.focus(), t.click()))(${q(`#f${f5} .toggle`)})`);
    check('a toggle keeps the keyboard focus across its redraw', await waitFor(`!${q(`#f${f5}.card`)} && document.activeElement === ${q(`#f${f5} .toggle`)}`),
        await js('document.activeElement.outerHTML.slice(0, 80)'));
    await js(`(t => (t.focus(), t.click()))(${q(`#f${f5} .toggle`)})`);
    check('and back', await waitFor(`!!${q(`#f${f5}.card`)} && document.activeElement === ${q(`#f${f5} .toggle`)}`));
    await js(`(r => (r.selectNodeContents(${q(`#f${f5} > .head .toggle`)}), getSelection().removeAllRanges(), getSelection().addRange(r)))(document.createRange())`);
    await js(`${q(`#f${f5} > .head`)}.dispatchEvent(new MouseEvent('click', { bubbles: true, detail: 1 }))`);
    await sleep(200);
    check('a mouse click ending a text selection in a head does not collapse it', await js(`!!${q(`#f${f5}.card`)}`), await hash());
    await js(`(t => (t.focus(), t.click()))(${q(`#f${f5} > .head .toggle`)})`);
    check('a keyboard click on the toggle collapses it despite a leftover selection', await waitFor(`!${q(`#f${f5}.card`)}`), await hash());
    await js('getSelection().removeAllRanges()');
    await js(`${q(`#f${f5} .toggle`)}.click()`);
    await waitFor(`!!${q(`#f${f5}.card`)}`);
    await click(`li[data-node="f${f4}"] > .node > .link`);
    check('a tree file click expands it in place and scrolls to it', await waitFor(`!!${q(`#f${f4}.card`)} && !!${q(`#f${f5}.card`)} && (r => r.top >= 0 && r.top < innerHeight / 2)(${q(`#f${f4}`)}.getBoundingClientRect())`), await hash());

    // A commit's panel stands in for its files, right under its summary; the
    // files' state stays, to show again when it closes.
    await load(`#at=c${c2}&c${c2}=f${f4},s${s22}~d,f${f5}~d`);
    await waitFor(`!!${q(`#f${f5} .panel table.diff`)} && !!${q(`#s${s22} .panel table.diff`)}`);
    const twoFiles = [[f4, `,s${s22}~d`], [f5, '~d']].sort((a, b) => a[0] - b[0]).map(([f, p]) => `f${f}${p}`).join();
    const commitPanel = `(c => !!c.querySelector(':scope > .panel table.diff') && ${prevShown(`c.querySelector(':scope > .panel')`)}.tagName === 'DETAILS'
        && !c.querySelector('.files, .file') && ![...c.querySelectorAll(':scope > h3')].some(h => h.textContent.startsWith('Files')))(${q(`#c${c2} > .card`)})`;
    const listShown = `!${q(`#c${c2} > .card > .panel`)} && !!${q(`#f${f4}.card`)} && !!${q(`#s${s22}.card .panel table.diff`)} && !!${q(`#f${f5}.card .panel table.diff`)}`;
    check('a commit shows its list tab by default; it has no diff widget', await tabsOf(`#c${c2} > .card`) === 'list*|diff'
        && await js(`[...document.querySelectorAll('#c${c2} > .card > .head .widgets button')].every(b => b.textContent !== 'diff')`) && !await js(`${q(`li[data-node="c${c2}"] > .node .widgets`)}.textContent.includes('diff')`), await tabsOf(`#c${c2} > .card`));
    await js(`${tab(`#c${c2} > .card`, 'diff')}.click()`);
    check("its diff tab replaces its files, right under its summary, keeping their state", await waitFor(`${param('c' + c2)} === 'd,${twoFiles}' && ${commitPanel}`), await hash());
    check('the diff tab shows pressed', await tabsOf(`#c${c2} > .card`) === 'list|diff*', await tabsOf(`#c${c2} > .card`));
    const tabDepth = await js('history.length');
    await js(`${tab(`#c${c2} > .card`, 'diff')}.click()`);
    check('the pressed tab again changes nothing', await js(`${param('c' + c2)} === 'd,${twoFiles}' && history.length === ${tabDepth} && ${commitPanel}`), await hash());
    const panelOpen = await hash();
    check('reload restores the diff tab over its files', await load(panelOpen) && await waitFor(commitPanel) && await tabsOf(`#c${c2} > .card`) === 'list|diff*', panelOpen);
    await js(`${tab(`#c${c2} > .card`, 'list')}.click()`);
    check('the list tab restores the same expanded files and sections', await waitFor(`${param('c' + c2)} === '${twoFiles}' && ${listShown}`), await hash());
    check('the list tab shows pressed', await tabsOf(`#c${c2} > .card`) === 'list*|diff', await tabsOf(`#c${c2} > .card`));
    await js('history.back()');
    check('back shows the diff tab again', await waitFor(commitPanel) && await tabsOf(`#c${c2} > .card`) === 'list|diff*', await hash());
    await js(`${tab(`#c${c2} > .card`, 'diff')}.focus()`);
    await arrow('ArrowLeft');
    check('an arrow key moves to the list tab, which keeps the focus', await waitFor(`${param('c' + c2)} === '${twoFiles}' && ${listShown}`)
        && await waitFor(`document.activeElement === ${tab(`#c${c2} > .card`, 'list')}`) && await tabsOf(`#c${c2} > .card`) === 'list*|diff',
        await js('document.activeElement.outerHTML.slice(0, 80)'));
    await arrow('ArrowRight');
    check('and back to the diff tab', await waitFor(`${commitPanel} && document.activeElement === ${tab(`#c${c2} > .card`, 'diff')}`)
        && await tabsOf(`#c${c2} > .card`) === 'list|diff*', await js('document.activeElement.outerHTML.slice(0, 80)'));
    await click(`li[data-node="s${s23}"] > .node > .link`);
    check('a tree target in the commit closes its diff and shows the target', await waitFor(`!${param('c' + c2)}.startsWith('d') && !!${q(`#s${s23}.card .panel table.diff`)} && !!${q(`#s${s22}.card`)}`), await hash());
    await load(panelOpen);
    await waitFor(commitPanel);
    await js(`[...document.querySelectorAll('#summary .flag-list button')].find(b => b.textContent.startsWith('greet returns')).click()`);
    check('a flag link in the commit closes its diff and shows the flagged line', await waitFor(`!${param('c' + c2)}.startsWith('d') && !!${q(`.panel table.code-view[data-fid="${f4}"] .flag-icon[data-flag="${flag1}"]`)}`), await hash());

    // ---- a file's tabs ----
    // An expanded file's head carries list | diff | file, as a commit's
    // carries list | diff; diff or file shows in place of its sections, whose
    // state stays for the list tab.
    const fileTabsOf = fid => tabsOf(`#f${fid}`);
    const sectionsShown = `!${q(`#f${f4} > .panel`)} && !!${q(`#f${f4} > .sections`)} && !!${q(`#s${s22}.card .panel table.diff`)}`;
    const filePanel = cls => `(c => !!c.querySelector(':scope > .panel table.${cls}:not([data-sid])') && !c.querySelector('.sections, .section')
        && ${prevShown(`c.querySelector(':scope > .panel')`)}.matches('.prose, .missing, .flag-slot') && ![...c.querySelectorAll(':scope > h3')].some(h => h.textContent === 'Sections'))(${q(`#f${f4}`)})`;
    await load(`#at=f${f4}&c${c2}=f${f4},s${s22}~d`);
    await waitFor(sectionsShown);
    check('a file shows its list tab by default, its sections listed', await fileTabsOf(f4) === 'list*|diff|file', await fileTabsOf(f4));
    check('its head has no diff or file widget, nor its tree node, and nothing closes it', await js(`[...document.querySelectorAll('#f${f4} > .head .widgets button')].every(b => !['diff', 'file'].includes(b.textContent))
        && ![...document.querySelectorAll('li[data-node="f${f4}"] > .node .widgets button')].some(b => ['diff', 'file'].includes(b.textContent))
        && !document.querySelector('#commits .tool.close')`));
    check('a section keeps its diff and file widgets', await js(`!!${tool(`#s${s22}`, 'diff')} && !!${tool(`#s${s22}`, 'file')}`));
    await js(`${tab(`#f${f4}`, 'diff')}.click()`);
    check("its diff tab replaces its sections, keeping their state", await waitFor(`${param('c' + c2)} === 'f${f4}~d,s${s22}~d' && ${filePanel('diff')}`)
        && await fileTabsOf(f4) === 'list|diff*|file', await hash());
    await js(`${tab(`#f${f4}`, 'file')}.click()`);
    check('its file tab shows the whole file in their place', await waitFor(`${param('c' + c2)} === 'f${f4}~v,s${s22}~d' && ${filePanel('code-view')}`)
        && await fileTabsOf(f4) === 'list|diff|file*', await hash());
    await shot('10b-file-tabs');
    const fileTabOpen = await hash();
    check('reload restores the file tab', await load(fileTabOpen) && await waitFor(filePanel('code-view')) && await fileTabsOf(f4) === 'list|diff|file*', fileTabOpen);
    await js(`${tab(`#f${f4}`, 'list')}.click()`);
    check('the list tab shows the same expanded sections', await waitFor(`${param('c' + c2)} === 'f${f4},s${s22}~d' && ${sectionsShown}`)
        && await fileTabsOf(f4) === 'list*|diff|file', await hash());
    await js('history.back()');
    check('back shows the file tab again', await waitFor(filePanel('code-view')) && await fileTabsOf(f4) === 'list|diff|file*', await hash());
    await js(`${tab(`#f${f4}`, 'file')}.focus()`);
    await arrow('ArrowLeft');
    check("an arrow key moves to the file's diff tab, which keeps the focus", await waitFor(`${filePanel('diff')} && document.activeElement === ${tab(`#f${f4}`, 'diff')}`)
        && await fileTabsOf(f4) === 'list|diff*|file', await js('document.activeElement.outerHTML.slice(0, 80)'));
    await arrow('ArrowRight');
    await arrow('ArrowRight');
    check('and wraps around to its list tab', await waitFor(`${sectionsShown} && document.activeElement === ${tab(`#f${f4}`, 'list')}`)
        && await fileTabsOf(f4) === 'list*|diff|file', await hash());
    await load(`#at=f${f4}&c${c2}=f${f4}~d,s${s22}~d`);
    await waitFor(filePanel('diff'));
    await click(`li[data-node="s${s23}"] > .node > .link`);
    check('a tree target in the file switches it to its list and shows the target', await waitFor(`${param('c' + c2)} === 'f${f4},s${s22}~d,s${s23}~d' && !!${q(`#s${s23}.card .panel table.diff`)} && !!${q(`#s${s22}.card`)}`)
        && await fileTabsOf(f4) === 'list*|diff|file', await hash());
    // Fewer than two meaningful tabs show none; each kind its own.
    const fBin = fileOf(C1, 'data.bin');
    const fLogo = fileOf(C1, 'share/logo.png');
    await load(`#at=f${fBin}&c${c1}=f${fBin}~d,f${fLogo}~v`);
    check('a binary file has no tabs, and a diff given it in the URL shows nothing', await waitFor(`!!${q(`#f${fBin}.card`)} && !!${q(`#f${fLogo} > .panel img`)}`)
        && await js(`!${q(`#f${fBin} .tabs`)} && !${q(`#f${fBin} > .panel`)}`), await hash());
    check('an image, with no sections, has diff and file, its file tab its picture', await fileTabsOf(fLogo) === 'diff|file*', await fileTabsOf(fLogo));
    await load(`#at=f${fTodo}&c${c3}=f${fTodo}~v`);
    check('a deleted file with no sections has only its diff, so no tabs, and a file tab given it shows its diff',
        await waitFor(`!!${q(`#f${fTodo} > .panel table.diff`)}`) && await js(`!${q(`#f${fTodo} .tabs`)}`), await hash());
    // Steps switch a file's tab; leaving one restores the reader's.
    await load(`#g=3&c${c2}=f${f4}~v`);
    await waitFor(filePanel('code-view'));
    await click('#guide_next');
    check("a section step switches its file to its list", await waitFor(`${param('g')} === '4' && !!${q(`#s${s23}.card .panel table.diff tr.hl`)}`)
        && await fileTabsOf(f4) === 'list*|diff|file', await hash());
    await click('#guide_exit');
    check("leaving it restores the reader's file tab", await waitFor(`${param('g')} === null && ${param('c' + c2)} === 'f${f4}~v' && ${filePanel('code-view')}`), await hash());
    await load(`#g=10&c${c2}=f${f4},s${s22}~d`);
    await waitFor(sectionsShown);
    await click('#guide_next');
    check("a file diff step switches the file to its diff tab", await waitFor(`${param('g')} === '11' && ${filePanel('diff')}`)
        && await fileTabsOf(f4) === 'list|diff*|file', await hash());
    await click('#guide_exit');
    check('and leaving it restores the list, its sections as they were', await waitFor(`${param('g')} === null && ${param('c' + c2)} === 'f${f4},s${s22}~d' && ${sectionsShown}`), await hash());

    // A step undoes what it changed, and only that, when left.
    await load(`#g=6&c${c2}=d`);
    await click('#guide_next');
    check('a guide step into the commit switches it to its list and shows the file', await waitFor(`${param('g')} === '7' && !${q(`#c${c2} > .card > .panel`)} && !!${q(`.panel table.code-view[data-fid="${f5}"] tr.hl`)}`)
        && await tabsOf(`#c${c2} > .card`) === 'list*|diff', await hash());
    check('a view step shows the file on its file tab', await tabsOf(`#f${f5}`) === 'list|diff|file*', await tabsOf(`#f${f5}`));
    await click('#guide_next');
    check('next collapses the file the step expanded and restores the diff tab', await waitFor(`${param('g')} === '8' && ${param('c' + c2)} === 'd' && ${commitPanel}`)
        && await tabsOf(`#c${c2} > .card`) === 'list|diff*', await hash());
    await load(`#g=6&c${c2}=f${f5}`);
    await click('#guide_next');
    await waitFor(`${param('g')} === '7' && !!${q(`#f${f5}.card .panel table.code-view`)}`);
    await click('#guide_next');
    check('a file the reader opened stays open across a step into it', await waitFor(`${param('g')} === '8' && ${param('c' + c2)} === 'f${f5}' && !!${q(`#f${f5}.card`)} && !${q(`#f${f5} .panel`)}`), await hash());
    check('a commit diff step switches it to its diff tab', await waitFor(`${param('c' + c3)}?.split(',')[0] === 'd' && !!${q(`#c${c3} > .card > .panel table.diff`)} && !${q(`#c${c3} .files`)}`)
        && await tabsOf(`#c${c3} > .card`) === 'list|diff*', await hash());
    await load(`#g=7&c${c3}=f${f7}`);
    await click('#guide_next');
    check('a commit diff step keeps the files the reader expanded', await waitFor(`${param('c' + c3)} === 'd,f${f7}' && !${q(`#f${f7}`)}`), await hash());
    await click('#guide_next');
    check('and leaving it shows them again', await waitFor(`${param('g')} === '9' && ${param('c' + c3)} === 'f${f7}' && !!${q(`#f${f7}.card`)}`), await hash());
    await load(`#g=6&c${c2}=d,f${f4}`);
    await click('#guide_next');
    await waitFor(`${param('g')} === '7' && !!${q(`#f${f5}.card .panel table.code-view`)}`);
    const inStep = await hash();
    await load(inStep);
    await waitFor(`!!${q(`#f${f5}.card .panel table.code-view`)}`);
    await click('#guide_exit');
    check("exit, after a reload, restores the reader's layout", await waitFor(`${param('g')} === null && ${param('u')} === null && ${param('c' + c2)} === 'd,f${f4}' && ${commitPanel}`), await hash());
    await load(`#at=c${c2}&c${c2}=f${f4},f${f5}~d`);
    await waitFor(`!!${q(`#f${f5} .panel table.diff`)}`);
    await js(`window.kept = ${q(`#f${f5}`)}`);
    await js(`${tab(`#f${f4}`, 'diff')}.click()`);
    await waitFor(`!!${q(`#f${f4} .panel table.diff`)}`);
    check("a file's panel redraws only that file", await js('window.kept.isConnected'));
    // Collapsing a long file from its head stuck over the top leaves its row
    // where the head was.
    await js(`(r => window.scrollTo(0, r.top + scrollY + 200))(${q(`#f${f4}`)}.getBoundingClientRect())`);
    const [stuck, stuckAt] = await js(`(c => (h => [c.getBoundingClientRect().top < h.top - 50, h.top])(c.querySelector(':scope > .head').getBoundingClientRect()))(${q(`#f${f4}`)})`);
    await click(`#f${f4} > .head`);
    check('collapsing a stuck head shows the collapsed row where the head was', stuck && await waitFor(`!${q(`#f${f4}.card`)} && Math.abs(${q(`#f${f4}`)}.getBoundingClientRect().top - ${stuckAt}) < 1`),
        { stuck, stuckAt, top: await js(`${q(`#f${f4}`)}.getBoundingClientRect().top`) });

    // ---- sections expand in place ----
    const fBar = fileOf(commit('Add Bar, a counter'), 'lib/Bar.pm');
    const barSecs = await sections(fBar);
    const [sStart, sStop, sTotal] = ['start', 'stop', 'total'].map(n => barSecs.find(s => s.name === `sub ${n}`).id);
    const toggle = sid => q(`#s${sid} .toggle`);
    await load(`#at=f${fBar}&c${c9}=f${fBar}`);
    check('a file lists every section as a collapsed row', await js(`document.querySelectorAll('#f${fBar} .sections > .section').length`) === barSecs.length
        && await js(`!${q(`#f${fBar} .section.card`)}`));
    check('a row shows name and counts', await js(`(r => r.textContent.includes('start') && !!r.querySelector('.counts') && !r.querySelector('.meta'))(${q(`#s${sStart} .row`)})`));
    // A =head1 is a section of its own, its sub-headings left with their subs.
    const head1 = barSecs.find(s => s.name === '=head1 NAME');
    check('a POD =head1 is a section, less the docs of the subs it names', !!head1 && head1.ranges.length === 1 && head1.ranges[0][1] < barSecs.find(s => s.id === sStart).doc_ranges[0][0], barSecs);
    // Code between subs is a section the agent named.
    const sLimit = barSecs.find(s => s.name === 'use constant LIMIT')?.id;
    check('code between subs shows under the name the agent gave it, in its row and the tree',
        !!sLimit && await js(`${q(`#s${sLimit} .toggle`)}?.textContent.endsWith('use constant LIMIT') && ${q(`li[data-node="s${sLimit}"] > .node > .link`)}?.textContent.trim() === 'use constant LIMIT'`), barSecs);
    check('a file card takes the file hue and keeps room on its right', await js(`(c => getComputedStyle(c).getPropertyValue('--lv-line').trim() === getComputedStyle(document.documentElement).getPropertyValue('--lv-file-line').trim()
        && parseFloat(getComputedStyle(c).paddingRight) > 0)(${q(`#f${fBar}.card`)})`));
    let before = await js(`${toggle(sStart)}.getBoundingClientRect().top`);
    await click(`#s${sStart} .toggle`);
    check('a row expands in place with its diff', await waitFor(`!!${q(`#s${sStart}.card .panel table.diff[data-sid="${sStart}"]`)}`), await hash());
    const afterStart = await js(`${toggle(sStart)}.getBoundingClientRect().top`);
    check('the expanded row holds still', Math.abs(afterStart - before) < 1, { before, afterStart });
    // A reader clicks a row they can see; how far below the first row's diff
    // the next row lands depends on the header's height (it wraps on narrow
    // windows), so bring it on screen first. A row off screen has nothing to
    // hold and goes to the top.
    await js(`window.scrollTo(0, ${toggle(sStop)}.getBoundingClientRect().top + scrollY - 300)`);
    before =await js(`${toggle(sStop)}.getBoundingClientRect().top`);
    await click(`#s${sStop} .toggle`);
    check('a second section expands beside the first', await waitFor(`!!${q(`#s${sStop}.card .panel table.diff`)} && !!${q(`#s${sStart}.card`)}`), await hash());
    const afterStop = await js(`${toggle(sStop)}.getBoundingClientRect().top`);
    check('the second row holds still', Math.abs(afterStop - before) < 1, { before, afterStop });
    await shot('10-sections-expanded');
    await js(`${tool(`#s${sStart}`, 'file')}.click()`);
    check("a section head's widgets do not collapse it", await waitFor(`!!${q(`#s${sStart}.card .panel table.code-view`)}`), await hash());
    await js(`${tool(`#s${sStart}`, 'diff')}.click()`);
    await waitFor(`!!${q(`#s${sStart}.card .panel table.diff`)}`);
    await click(`#s${sStart} > .head`);
    check('clicking its head collapses only that section', await waitFor(`!${q(`#s${sStart}.card`)} && !!${q(`#s${sStart} .toggle`)} && !!${q(`#s${sStop}.card`)}`), await hash());
    await js('history.back()');
    check('back expands it again', await waitFor(`!!${q(`#s${sStart}.card`)} && !!${q(`#s${sStop}.card`)}`), await hash());
    const twoOpen = await hash();
    check('reload restores the expanded sections', await load(twoOpen) && await waitFor(`!!${q(`#s${sStart}.card .panel table.diff`)} && !!${q(`#s${sStop}.card .panel table.diff`)}`), twoOpen);
    await click(`li[data-node="s${sTotal}"] > .node > .link`);
    check('a tree section click expands it and scrolls to it', await waitFor(`!!${q(`#s${sTotal}.card .panel table.diff`)} && !!${q(`#s${sStart}.card`)} && (r => r.top >= 0 && r.top < innerHeight)(${q(`#s${sTotal}`)}.getBoundingClientRect())`), await hash());
    await load(`#at=f${f4}&c${c2}=f${f4}`);
    await js(`[...document.querySelectorAll('#f${f4} .section .toggle')].find(b => b.textContent.endsWith('sub greet')).click()`);
    check('a section row opens its diff', await waitFor(`!!${q(`#s${s23} .panel table.diff[data-sid="${s23}"]`)}`), await hash());
    await click(`li[data-node="s${s22}"] > .node > .link`);
    check('tree section click opens its diff', await waitFor(`!!${q(`#s${s22} .panel table.diff[data-sid="${s22}"]`)} && !!${q(`#s${s23}.card`)}`), await hash());

    // ---- section diffs ----
    // Top-level and unit sections each show only their own lines; a
    // section the commit did not change shows no diff.
    for (const [c, f] of [[c1, f1], [c2, f4]]) {
        for (const s of await sections(f)) {
            await load(`#at=s${s.id}&c${c}=f${f},s${s.id}~d`);
            const sel = `#s${s.id} .panel table.diff`;
            if (!s.added && !s.removed) {
                check(`${s.name} diff: unchanged, no lines`, await waitFor(`${q(`#s${s.id} .panel`)}?.textContent === 'No line changes.'`));
                continue;
            }
            if (!await waitFor(`!!${q(sel)}`)) {
                check(`${s.name} diff shows`, false, await hash());
                continue;
            }
            // New line numbers, one list per hunk.
            const hunks = await js(`[...document.querySelectorAll('${sel} tr')].reduce((h, r) => (r.classList.contains('hunk') ? h.push([]) : r.dataset.new && h.at(-1).push(Number(r.dataset.new)), h), [])`);
            const own = n => [...s.ranges, ...s.doc_ranges].some(([a, b]) => n >= a && n <= b);
            check(`${s.name} diff shows only its lines`, hunks.flat().length > 0 && hunks.flat().every(own), hunks);
            check(`${s.name} diff hunks are unbroken`, hunks.every(h => h.every((n, i) => !i || n === h[i - 1] + 1)), hunks);
        }
    }

    // ---- a unit's diff: its code, then its documentation under __END__ ----
    const S22 = (await sections(f4)).find(s => s.id === s22);
    const unitRows = sel => js(`[...document.querySelectorAll(${JSON.stringify(sel + ' tr')})].map(r => r.classList.contains('part') ? 'part ' + r.textContent
        : r.classList.contains('hunk') ? 'hunk' : r.dataset.new ? Number(r.dataset.new) : 'old')`);
    const unitShape = rows => {
        const at = rows.findIndex(r => String(r).startsWith('part'));
        const lines = (from, to) => rows.slice(from, to).filter(r => typeof r === 'number');
        const span = ([a, b]) => Array.from({ length: b - a + 1 }, (_, k) => a + k);
        const [[a, b]] = S22.doc_ranges;
        return at > 0 && rows[at] === `part documentation — lines ${a}–${b}`
            && JSON.stringify(lines(0, at)) === JSON.stringify(S22.ranges.flatMap(span))
            && JSON.stringify(lines(at)) === JSON.stringify(S22.doc_ranges.flatMap(span));
    };
    await load(`#at=s${s22}&c${c2}=f${f4},s${s22}~d`);
    const unitSel = `#s${s22} .panel table.diff`;
    await waitFor(`!!${q(unitSel + ' tr.part')}`);
    let rows = await unitRows(unitSel);
    check('a unit diff shows its whole code, a separator, then its whole documentation, nothing between', unitShape(rows), { rows, S22 });
    await click('#diff_toggle button[data-value="split"]');
    await waitFor(`!!${q(unitSel + '.split tr.part')}`);
    rows = await unitRows(unitSel);
    check('side by side too', unitShape(rows), rows);
    const layout = () => js(`[...${q('#diff_toggle[role=radiogroup]')}.children].map(b => b.getAttribute('role') + ':' + b.getAttribute('aria-checked') + ':' + b.tabIndex).join()`);
    check('the diff layout switch is a radio group, side by side checked', await layout() === 'radio:false:-1,radio:true:0', await layout());
    await js(`${q('#diff_toggle button[data-value="split"]')}.focus()`);
    await arrow('ArrowRight');
    check('an arrow key picks unified, which takes the focus', await waitFor(`!!${q(unitSel + '.unified')} && document.activeElement === ${q('#diff_toggle button[data-value="unified"]')}`)
        && await layout() === 'radio:true:0,radio:false:-1', await layout());
    check('the documentation beside the summary is rendered POD', await waitFor(`${q(`#s${s22} .docs .pod h2`)}?.textContent === 'name' && ${q(`#s${s22} .docs .pod code`)}?.textContent === 'anon' && !${q(`#s${s22} .docs table`)}`));
    await shot('11-unit-diff');

    // ---- each level's header sticks while its level is on screen ----
    // Each header's [top, bottom, whether it is what shows at its middle].
    const heads = sels => js(`${JSON.stringify(sels)}.map(s => (e => (b => [b.top, b.bottom, e.contains(document.elementFromPoint(b.left + 5, (b.top + b.bottom) / 2))])(e.getBoundingClientRect()))(document.querySelector(s)))`);
    const headerH = () => js(`${q('#header')}.getBoundingClientRect().bottom`);
    const levels = [`#c${c2} > .card > .head`, `#f${f4} > .head`, `#s${s22} > .head`];
    const stacked = (top, hs) => hs.every(([t, b, shown], i) => shown && Math.abs(t - (i ? hs[i - 1][1] : top)) < 1 && b > t && b < innerH);
    const innerH = await js('innerHeight');
    await load(`#at=s${s22}&c${c2}=f${f4},s${s22}~d`);
    await waitFor(`!!${q(`#s${s22} .panel table.diff`)}`);
    await sleep(200);
    let hs = await heads(levels);
    check('a target scrolled to lands below the headers stuck over it', stacked(await headerH(), hs.slice(0, 2))
        && await js(`${q(`#s${s22}`)}.getBoundingClientRect().top`) >= hs[1][1] - 0.5, { hs, header: await headerH(), top: await js(`${q(`#s${s22}`)}.getBoundingClientRect().top`) });
    for (const mode of ['unified', 'split']) {
        await click(`#diff_toggle button[data-value="${mode}"]`);
        await waitFor(`!!${q(`#s${s22} .panel table.diff.${mode}`)}`);
        // Deep into the section: its bottom 250px from the top of the window.
        await js(`(r => window.scrollTo(0, r.bottom + scrollY - 250))(${q(`#s${s22}`)}.getBoundingClientRect())`);
        hs = await heads(levels);
        check(`${mode}: deep in a section, the commit, file and section headers stack under the page header`, stacked(await headerH(), hs), hs);
        await shot(`11-stuck-headers-${mode}`);
    }
    await click('#diff_toggle button[data-value="unified"]');
    await load(`#at=f${f4}&c${c2}=f${f4},f${f5}`);
    const [first] = await js(`[...document.querySelectorAll('#c${c2} .card[id^="f"]')].map(e => e.id)`);
    const headH = await js(`${q(levels[0])}.getBoundingClientRect().height`);
    await js(`(r => window.scrollTo(0, r.bottom + scrollY - ${await headerH()} - ${headH} + 2))(document.getElementById('${first}').getBoundingClientRect())`);
    hs = await heads([levels[0], `#${first} > .head`]);
    check('past a file, its header is gone and the commit label stays', stacked(await headerH(), hs.slice(0, 1)) && !hs[1][2] && hs[1][1] <= hs[0][1] + 0.5, hs);

    // ---- the page holds still while it scrolls ----
    // Wheel down the whole page and back up, over the main column, with every
    // commit's diff open. Each step must move the page (no box in it takes
    // the wheel), and what is under the pointer must move by exactly the
    // scroll. It takes up to 200 timed steps, so it runs for authors only
    // (AUTHOR_TESTING), not on installs.
    const wheelThrough = async () => {
        const bad = [];
        for (const dir of [1, -1]) {
            for (let i = 0; i < 100; i++) {
                const [y0, top0, max] = await js(`(e => (window.probe = e, [scrollY, e.getBoundingClientRect().top, document.documentElement.scrollHeight - innerHeight]))(document.elementFromPoint(700, 450))`);
                if (dir > 0 ? y0 >= max : y0 <= 0) break;
                await send('Input.dispatchMouseEvent', { type: 'mouseWheel', x: 700, y: 450, deltaX: 0, deltaY: dir * 300 });
                await sleep(100);
                const [y1, top1] = await js(`[scrollY, window.probe.isConnected ? window.probe.getBoundingClientRect().top : null]`);
                const jump = top1 == null ? null : top1 - top0 + y1 - y0;
                if (y1 === y0 || jump == null || Math.abs(jump) > 2) bad.push({ y0, y1, jump });
            }
        }
        return bad;
    };
    const wheelName = 'wheel scrolls the page without jumps, top to bottom and back';
    // AUTHOR_TESTING unset, empty or 0 is off.
    if (!['', '0'].includes(process.env.AUTHOR_TESTING ?? '')) {
        await load(`#${[c1, c2, c3, c8, c9].map(c => `c${c}=d`).join('&')}`);
        await waitFor(`!document.querySelector('.panel .missing') && document.querySelectorAll('.panel table.diff').length > 5`);
        const stalls = await wheelThrough();
        check(wheelName, stalls.length === 0, stalls);
    }
    else console.log(`ok ${++count} - ${wheelName} # skip AUTHOR_TESTING is off`);

    // ---- scroll tracking and back/forward ----
    await load();
    await js(`window.scrollTo(0, document.getElementById('c${c3}').offsetTop)`);
    check('scrolling marks the current commit in the tree', await waitFor(`!!${q(`#tree li.current[data-node="c${c3}"]`)}`), await js(`${q('#tree li.current')}?.dataset.node`));
    await js(`window.scrollTo(0, 0)`);
    check('scrolling back marks the summary', await waitFor(`!!${q('#tree li.current[data-node="summary"]')}`));
    await click(`#f${f5} .toggle`);
    check('file row opens the file', await waitFor(`!!${q(`#f${f5}.card`)}`));
    await js('history.back()');
    check('back closes it', await waitFor(`!${q(`#f${f5}.card`)} && !!${q(`#f${f5} .toggle`)} && !!${q(`#c${c2} .card`)}`));
    await js('history.forward()');
    check('forward reopens it', await waitFor(`!!${q(`#f${f5}.card`)}`));

    // ---- an iteration with no note, flags or guide ----
    check('earlier iteration loads', await load('#i=1'));
    check('summary without note or flags shows no null', await js(`(s => !s.innerText.includes('null') && !s.querySelector('.note, .flag-list'))(${q('#summary')})`),
        await js(`${q('#summary')}.innerText.slice(0, 200)`));
    check('no guide without steps', await js(`${q('#guide_play')}.hidden`));
    check('with no guide and no flags, the header shows no empty group and no stray line', await js(`${q('#head_nav')}.hidden
        && getComputedStyle(${q('#head_view')}).borderLeftStyle === 'none' && parseFloat(getComputedStyle(${q('#head_view')}).paddingLeft) === 0`),
        await js(`(v => [${q('#head_nav')}.hidden, v.borderLeftStyle, v.paddingLeft])(getComputedStyle(${q('#head_view')}))`));
    // Feedback written on an earlier iteration belongs to it, even on a
    // commit the latest iteration no longer has.
    const R1 = await js(`fetch('api/review?iteration=1').then(r => r.json())`);
    const cOld = R1.commits.find(c => c.subject === 'Add old.js');
    const fOld = fileOf(cOld, 'js/old.js');
    await load(`#i=1&at=f${fOld}&c${cOld.id}=f${fOld}`);
    await click(`#f${fOld} .head .widgets button[title="Add feedback for the agent"]`);
    await js(`${q(`#f${fOld} .form-slot form textarea`)}.value = 'Only in iteration 1.'; ${q(`#f${fOld} .form-slot form`)}.requestSubmit()`);
    check('feedback posts on an earlier iteration', await waitFor(`${q(`#f${fOld} .notes`)}.textContent.includes('Only in iteration 1.')`));
    ex = await exportData();
    check('and is stored against that iteration', ex.feedback.some(f => f.body === 'Only in iteration 1.' && f.iteration === 1 && f.commit === cOld.revision), ex.feedback.at(-1));

    // ---- merges ----
    // A panel a commit has no tab for (a hand-made URL) shows its list.
    await load(`#c${c7}=d&c${c2}=r`);
    check('a merge given a diff panel shows its commits, with no tabs', await waitFor(`${q(`#c${c7} .commit-list`)}?.children.length === 2 && !${q(`#c${c7} .panel`)}`)
        && await js(`!${q(`#c${c7} .tabs`)}`), await hash());
    check('a commit given a resolution panel shows its files, list shown', await js(`!${q(`#c${c2} > .card > .panel`)} && !!${q(`#c${c2} .files`)}`)
        && await tabsOf(`#c${c2} > .card`) === 'list*|diff', await hash());
    await load();
    const visible = sel => `(n => !!n && n.offsetParent !== null)(${q(sel)})`;
    check('mainline sections only', await js(`[...document.querySelectorAll('section.commit')].map(s => s.id).join()`) === [c1, c2, c3, c7, c8, c9].map(c => 'c' + c).join());
    check('merge badge rolls up its commits\' flags', await js(`(b => b && b.classList.contains('flag-red') && b.textContent)(${q(`li[data-node="c${c7}"] > .node .badge`)})`) === '2');
    check('nested merge badge', await js(`(b => b && b.classList.contains('flag-yellow') && b.textContent)(${q(`li[data-node="c${c6}"] > .node .badge`)})`) === '1');
    check('merge children hidden until current', !(await js(visible(`li[data-node="c${c4}"]`))));
    await click(`li[data-node="c${c7}"] > .node > .link`);
    check('merge node expands to its commits in order', await waitFor(`${visible(`li[data-node="c${c4}"]`)} && ${visible(`li[data-node="c${c6}"]`)}`)
        && await js(`[...${q(`li[data-node="c${c7}"] > ul`)}.children].map(li => li.dataset.node).join()`) === `c${c4},c${c6}`
        && await js(`!!${q(`li[data-node="c${c6}"] > ul > li[data-node="c${c5}"]`)}`));
    const mergeBadges = () => js(`[...document.querySelectorAll('#c${c7} .commit-list > .level:not(.card)')].map(r => r.querySelector('.badge')?.textContent ?? '')`);
    check("its commit rows carry their open flags' badges", JSON.stringify(await mergeBadges()) === JSON.stringify(['1', '1']), await mergeBadges());
    check('merge card lists its commits as rows with their summaries', await js(`(c => c.querySelectorAll('.commit-list > .level:not(.card)').length === 2 && c.textContent.includes('Documents Foo for new readers.') && c.textContent.includes('Brings in the changelog.'))(${q(`#c${c7} .card`)})`));
    check('a merge with no resolution has no tabs, and no diff widget', await js(`!${q(`#c${c7} > .card > .head .tabs`)}`)
        && await js(`![...document.querySelectorAll('#c${c7} .card .head .widgets button')].some(b => b.textContent === 'diff')`), await hash());
    check('a merge with no resolutions has no badge', await js(`!${q(`#c${c7} .resolved`)} && !${q(`li[data-node="c${c7}"] .resolved`)}`));
    // Opening a commit in a merge expands its row in place: the merge keeps
    // its summary and its other commits, and the page does not move.
    await js(`${q(`#c${c4} .toggle`)}.scrollIntoView({ block: 'center' })`);
    const [rowAt, rowY] = await js(`[${q(`#c${c4}`)}.getBoundingClientRect().top, scrollY]`);
    check("a merge's commit row reads as a closed row", await js(`(r => r.matches('.level:not(.card)') && r.querySelector('.toggle').getAttribute('aria-expanded') === 'false' && r.querySelector('.toggle').textContent === '▸ 4. Add a README')(${q(`#c${c4}`)})`));
    await js(`${q(`#c${c4} .toggle`)}.click()`);
    check('a commit row expands in place in its merge', await waitFor(`${param('c' + c7)} === 'c${c4}' && !!${q(`#c${c7} > .card > .commit-list > #c${c4}.card`)}`), await hash());
    await settled();
    const opened = await js(`({ head: ${q(`#c${c4} > .head`)}.getBoundingClientRect().top, y: scrollY, merge: !!${q(`#c${c7} > .card > .head h2`)} && ${q(`#c${c7} > .card`)}.textContent.includes('Merges the docs branch'),
        siblings: [...${q(`#c${c7} .commit-list`)}.children].map(e => e.id + (e.matches('.card') ? '+' : '')).join() })`);
    check("opening a merge's commit keeps the merge and its other commits shown", opened.merge && opened.siblings === `c${c4}+,c${c6}`, opened);
    check("opening a merge's commit leaves it where it was clicked", Math.abs(opened.head - rowAt) < 2 && Math.abs(opened.y - rowY) < 2, { rowAt, rowY, opened });
    await js(`${tab(`#c${c4}`, 'diff')}.click()`);
    check("a merged-in commit's diff tab shows its diff and leaves it open", await waitFor(`${param('c' + c7)} === 'c${c4}' && ${param('c' + c4)} === 'd'
        && !!${q(`#c${c7} #c${c4}.card > .panel table.diff`)}`) && await tabsOf(`#c${c4}`) === 'list|diff*', await hash());
    await js(`${tab(`#c${c4}`, 'list')}.click()`);
    check('and its list tab shows its files again', await waitFor(`${param('c' + c7)} === 'c${c4}' && ${param('c' + c4)} === null && !${q(`#c${c4} > .panel`)} && !!${q(`#c${c4} #f${f8}`)}`)
        && await tabsOf(`#c${c4}`) === 'list*|diff', await hash());
    check("a commit head's tooltip is its label", await js(`${q(`#c${c4} > .head h2`)}.title === '4. Add a README'`));
    check('opened commit expands in the tree', await waitFor(`${visible(`li[data-node="f${f8}"]`)} && ${q(`li[data-node="c${c4}"]`)}.classList.contains('open')`));
    await click(`#f${f8} .toggle`);
    check('a merged-in commit expands its file in place', await waitFor(`${param('c' + c4)} === 'f${f8}' && !!${q(`#c${c7} #c${c4}.card #f${f8}.card`)}`), await hash());
    check('no level has a close button', await js(`!document.querySelector('#commits .tool.close')`));
    const z = sel => js(`Number(getComputedStyle(${q(sel)}).zIndex)`);
    const [zOuter, zInner, zHead] = [await z(`#c${c7} > .card > .head`), await z(`#c${c4} > .head`), await z(`#f${f8} > .head`)];
    check('a commit head slides under the merge head, and a file head under the commit head', zOuter > zInner && zInner > zHead, [zOuter, zInner, zHead]);
    // The outer merge head, the highest, scrolled under the guide bar stays under it.
    const underBar = await js(`(() => {
        const bar = ${q('#guide_bar')}, mergeHead = ${q(`#c${c7} > .card > .head`)};
        bar.hidden = false;
        window.scrollBy(0, mergeHead.getBoundingClientRect().top - bar.getBoundingClientRect().top - 4);
        const c = mergeHead.getBoundingClientRect(), b = bar.getBoundingClientRect();
        const y = Math.max(c.top, b.top) + 1;
        const hit = y < Math.min(c.bottom, b.bottom) && document.elementFromPoint(c.left + c.width / 2, y);
        bar.hidden = true;
        return !!hit && bar.contains(hit) || JSON.stringify([c.top, c.bottom, b.top, b.bottom, scrollY, hit && hit.outerHTML.slice(0, 80)]);
    })()`);
    check('the guide bar paints over a merge head', underBar === true, underBar);
    const drilledFile = await hash();
    await load(`#c${c7}=c${c6}&c${c6}=c${c5}`);
    await waitFor(`!!${q(`#c${c5} > .head`)}`);
    const [zMerge, zNested, zDeep] = [await z(`#c${c7} > .card > .head`), await z(`#c${c6} > .head`), await z(`#c${c5} > .head`)];
    check('a nested merge head slides under the outer one, and its commit head under it', zMerge > zNested && zNested > zDeep, [zMerge, zNested, zDeep]);
    await load(drilledFile);
    check("a file head's tooltip is its path and status", await js(`${q(`#f${f8} > .head .toggle`)}.title === 'README.md (added)'`));
    const pill = sel => `(s => [s.borderTopStyle, s.borderLeftWidth, s.borderTopLeftRadius, s.borderBottomRightRadius])(getComputedStyle(${q(sel)}))`;
    const [secPill, filePill] = [await js(pill(`#f${f8} .section:not(.card)`)), await js(pill('.file:not(.card)'))];
    check('a collapsed section row is an outlined, rounded strip like a file row', secPill[0] === 'solid' && parseFloat(secPill[2]) > 0 && JSON.stringify(secPill) === JSON.stringify(filePill), { secPill, filePill });
    await js(`${q(`#f${f8} .section .toggle`)}.click()`);
    check('a section expands inside the file', await waitFor(`!!${q(`#c${c4} #f${f8} #s${s51}.card`)}`), await hash());
    check("the section head's tooltip is its name", await js(`(t => t.title !== '' && t.textContent === '▾ ' + t.title)(${q(`#s${s51} > .head .toggle`)})`));
    await shot('09-merge-drill');
    await js('history.back()');
    check('back collapses the section', await waitFor(`!${q(`#s${s51}.card`)} && !!${q(`#s${s51} .toggle`)} && !!${q(`#f${f8}.card`)}`));
    const drilled = await hash();
    check('reload restores merge → commit → file', await load(drilled) && await waitFor(`!!${q(`#c${c7} #c${c4} #f${f8}.card`)} && ${visible(`li[data-node="f${f8}"]`)}`), drilled);
    await click(`#f${f8} > .head`);
    check("the file's head collapses it", await waitFor(`!${q(`#f${f8}.card`)} && !!${q(`#f${f8} .toggle`)} && !!${q(`#c${c4}.card`)}`));
    await click(`#c${c4} > .head`);
    check("a merged-in commit's head collapses it to its row", await waitFor(`!${q(`#c${c4}.card`)} && !!${q(`#c${c7} .commit-list > #c${c4}.level:not(.card)`)} && ${param('c' + c7)} === null`), await hash());
    await js(`window.scrollTo(0, 0)`);
    await js(`window.scrollTo(0, document.getElementById('c${c7}').offsetTop)`);
    check('scrolling to a merge marks and expands it', await waitFor(`!!${q(`#tree li.current[data-node="c${c7}"]`)} && ${visible(`li[data-node="c${c6}"]`)}`));
    await js(`[...document.querySelectorAll('#summary .flag-list button')].find(b => b.textContent.startsWith('The changelog starts')).click()`);
    check('flag on a nested commit opens its merges then the file', await waitFor(`${param('c' + c5)} === 'f${f9}~v' && !!${q(`#c${c7} #c${c6} #c${c5} .panel table[data-fid="${f9}"] .flag-icon[data-flag="${flag7}"]`)}`), await hash());
    await click(`li[data-node="c${c7}"] > .node .widgets button[title="Add feedback for the agent"]`);
    await waitFor(`!!${q(`#c${c7} > .card > .form-slot form textarea`)}`);
    await js(`${q(`#c${c7} > .card > .form-slot form textarea`)}.value = 'Why a merge?'; ${q(`#c${c7} > .card > .form-slot form`)}.requestSubmit()`);
    check('feedback on a merge shows', await waitFor(`${q(`#c${c7} > .card > .notes`)}.textContent.includes('Why a merge?')`));
    ex = await exportData();
    check('merge feedback anchors to its revision', ex.feedback.some(f => f.body === 'Why a merge?' && f.target === 'commit' && f.commit === C7.revision));

    // A sixth level's content counts the six heads stuck over it (no
    // fixture nests that deep, so the nesting is built by hand).
    const sixth = await js(`(() => {
        let box = ${q('#commits')};
        const top = box;
        const made = [];
        for (let i = 0; i < 6; i++) { const card = Object.assign(document.createElement('div'), { className: 'card' }); card.append(Object.assign(document.createElement('div'), { className: 'head' })); box.append(card); made.push(card); box = card; }
        const body = box.appendChild(document.createElement('div'));
        const n = getComputedStyle(body).getPropertyValue('--cards').trim();
        made[0].remove();
        return n;
    })()`);
    check('content six levels deep counts six stuck heads', sixth === '6', sixth);
    // A long name in an open head ends in an ellipsis, not cut mid-letter.
    await load(`#c${c2}=f${f4}`);
    const long = await js(`(t => {
        t.lastChild.textContent = 'lib/Review/Helper/Very/Long/Path/Name/That/Goes/On/And/On/'.repeat(4) + 'Foo.pm';
        const s = getComputedStyle(t), title = t.parentElement.getBoundingClientRect(), r = t.getBoundingClientRect();
        return { cut: t.scrollWidth > t.clientWidth, ellipsis: s.textOverflow, overflow: s.overflowX, inside: r.right <= title.right + 0.5 };
    })(${q(`#f${f4} > .head .title > .toggle`)})`);
    check('a long name in an open head ends in an ellipsis', long.cut && long.ellipsis === 'ellipsis' && long.overflow === 'hidden' && long.inside, long);

    // ---- every level looks alike, but for its color ----
    // Closed, every kind is the same outlined, rounded row; open, the same
    // card with a left line; every title is one size, closed or open. A
    // merge's color is its own, on its card, its row and its tree node.
    const levelLook = `[...document.querySelectorAll('#commits .level')].map(e => (s => (t => ({ kind: e.dataset.kind, open: e.matches('.card'),
        line: s.borderLeftWidth + ' ' + s.borderLeftStyle, radius: e.matches('.card') ? s.borderTopLeftRadius : [s.borderTopLeftRadius, s.borderBottomRightRadius].join(),
        color: s.borderLeftColor, title: t.fontSize + ' ' + t.fontWeight }))(getComputedStyle(e.querySelector('.title'))))(getComputedStyle(e)))`;
    let looks = [];
    for (const hash of [`#c${c7}=c${c6}&c${c8}=p${encodeURIComponent('lib/Foo.pm')}`, `#c${c7}=c${c4}&c${c4}=f${f8},s${s51}~d&c${c2}=f${f4}`]) {
        await load(hash);
        await settled();
        looks = looks.concat(await js(levelLook));
    }
    const kindsSeen = state => [...new Set(looks.filter(l => l.open === state).map(l => l.kind))].sort().join();
    const same = (state, key) => new Set(looks.filter(l => l.open === state).map(l => l[key])).size === 1;
    check('every kind of level shows closed and open', kindsSeen(false) === 'commit,file,merge,section' && kindsSeen(true) === 'blob,commit,file,merge,section',
        [kindsSeen(false), kindsSeen(true)]);
    check('closed, every level is the same outlined, rounded row', same(false, 'line') && same(false, 'radius') && looks.find(l => !l.open).line.startsWith('3px solid'),
        looks.filter(l => !l.open));
    check('open, every level is a card with the same left line', same(true, 'line') && looks.find(l => l.open).line.startsWith('3px solid'), looks.filter(l => l.open));
    check('every title is one size, closed or open', new Set(looks.map(l => l.title)).size === 1, [...new Set(looks.map(l => l.title))]);
    const colorOf = (kind, open) => looks.find(l => l.kind === kind && l.open === open)?.color;
    check("a merge's color is its own, row and card alike", colorOf('merge', true) === colorOf('merge', false) && colorOf('commit', true) === colorOf('commit', false)
        && colorOf('merge', true) !== colorOf('commit', true), { merge: [colorOf('merge', true), colorOf('merge', false)], commit: [colorOf('commit', true), colorOf('commit', false)] });
    const treeLine = id => js(`getComputedStyle(${q(`li[data-node="${id}"] > .node`)}).borderLeftColor`);
    check('the tree gives merges and commits their colors', await treeLine(`c${c7}`) === colorOf('merge', true) && await treeLine(`c${c2}`) === colorOf('commit', true),
        [await treeLine(`c${c7}`), await treeLine(`c${c2}`)]);

    // ---- a merge that resolved conflicts ----
    // The server's resolution flag and remerge diff are stood in for, so the
    // page's side is checked on its own.
    const remerge = { files: [{ path: 'README.md', hunks: [{ old_start: 1, old_count: 3, new_start: 1, new_count: 1, lines: [
        { type: '-', text: '<<<<<<< HEAD', old: 1 }, { type: '-', text: '=======', old: 2 }, { type: '-', text: '>>>>>>> docs', old: 3 }, { type: '+', text: '# Foo', new: 1 }] }] }] };
    const { identifier: stub } = await send('Page.addScriptToEvaluateOnNewDocument', { source: `{
        const real = window.fetch;
        const json = x => new Response(JSON.stringify(x), { headers: { 'Content-Type': 'application/json' } });
        window.fetch = async (url, opts) => {
            const u = String(url);
            if (u === 'api/resolution/${c7}') return json(${JSON.stringify(remerge)});
            const res = await real(url, opts);
            if (!u.startsWith('api/review')) return res;
            const r = await res.json();
            r.identity = { user: 'ann', host: 'box', repo: 'Foo' };
            for (const c of r.commits) c.resolution = c.id === ${c7};
            return json(r);
        };
    }` });
    await load();
    check('the header shows whose review and which repository', await js(`(i => !i.hidden && i.textContent)(${q('#identity')})`) === 'ann@box · Foo'
        && await js(`${q('#title')}.textContent`) === 'Fixture review');
    check('a merge that resolved conflicts carries a badge, card and tree', await js(`!!${q(`#c${c7} .card .head .resolved`)} && !!${q(`li[data-node="c${c7}"] > .node .resolved`)}
        && !${q(`#c${c1} .head .resolved`)} && !${q(`li[data-node="c${c6}"] > .node .resolved`)}`));
    check('it has list and resolution tabs, list shown', await tabsOf(`#c${c7} > .card`) === 'list*|resolution', await tabsOf(`#c${c7} > .card`));
    await js(`${tab(`#c${c7}`, 'resolution')}.click()`);
    check('its resolution tab shows the remerge diff in place of its commits', await waitFor(`${param('c' + c7)} === 'r' && !${q(`#c${c7} .commit-list`)} && (p => p && p.querySelector('h4').textContent === 'README.md'
        && [...p.querySelectorAll('table.diff tr.del td.code')].map(t => t.textContent).join('|') === '<<<<<<< HEAD|=======|>>>>>>> docs'
        && p.querySelector('table.diff tr.add td.code').textContent === '# Foo')(${q(`#c${c7} .panel`)})`)
        && await tabsOf(`#c${c7} > .card`) === 'list|resolution*', await hash());
    await shot('12-merge-resolution');
    await js(`${tab(`#c${c7}`, 'list')}.click()`);
    check('its list tab shows its commits again', await waitFor(`${param('c' + c7)} === null && !${q(`#c${c7} .panel`)} && ${q(`#c${c7} .commit-list`)}?.children.length === 2`)
        && await tabsOf(`#c${c7} > .card`) === 'list*|resolution', await hash());
    await js(`${tab(`#c${c7}`, 'resolution')}.click()`);
    await waitFor(`!!${q(`#c${c7} .panel table.diff`)}`);
    await click('#diff_toggle button[data-value="split"]');
    check('side by side too', await waitFor(`!!${q(`#c${c7} .panel table.diff.split`)}`));
    await click('#diff_toggle button[data-value="unified"]');
    // A step that drills the merge closes its resolution; leaving the step
    // shows it again.
    await load(`#g=8&c${c7}=r`);
    await waitFor(`!!${q(`#c${c7} > .card > .panel table.diff`)}`);
    await click('#guide_next');
    check('a step drilling a merge replaces its resolution', await waitFor(`${param('g')} === '9' && ${param('c' + c7)} === 'c${c6}' && !!${q(`#c${c7} #c${c6} #c${c5}.card`)}`), await hash());
    await click('#guide_next');
    check('leaving it closes the drill and shows the resolution again', await waitFor(`${param('g')} === '10' && ${param('c' + c7)} === 'r' && ${param('c' + c6)} === null
        && !${q(`#c${c6}`)} && !!${q(`#c${c7} > .card > .panel table.diff`)}`) && await tabsOf(`#c${c7} > .card`) === 'list|resolution*', await hash());
    await send('Page.removeScriptToEvaluateOnNewDocument', { identifier: stub });

    // ---- a feedback or request form opens under its header ----
    // Clicking feedback or request on a tall card's header opens the form
    // right under that header, which stays where it is, stuck or not, and
    // stays put when the form is sent or cancelled.
    for (const [id, hash] of [[`c${c2}`, `#c${c2}=f${f4},s${s22}~d,f${f5}~d&c${c3}=d`], [`f${f4}`, `#c${c2}=f${f4},s${s22}~d,s${s23}~d&c${c3}=d`]]) {
        for (const stuck of [false, true]) {
            await load(hash);
            await settled();
            const steps = [];
            for (const [label, send] of [['feedback', true], ['request', false]]) {
                const isStuck = await placeHead(id, stuck);
                const top = await js(`${headOf(id)}.getBoundingClientRect().top`);
                await mouseClick(`[...${headOf(id)}.querySelectorAll('.widgets button')].find(b => b.textContent === '${label}')`);
                const shown = await waitFor(`!!${formUnder(id)}`);
                await sleep(100);
                const [opened, gap, focus] = await js(`(f => [${headOf(id)}.getBoundingClientRect().top, f.getBoundingClientRect().top - ${headOf(id)}.getBoundingClientRect().bottom,
                    f.contains(document.activeElement)])(${formUnder(id)})`);
                if (send) await js(`(f => { f.querySelector('textarea').value = 'Held under ${id}.'; f.requestSubmit() })(${formUnder(id)})`);
                else await js(`[...${formUnder(id)}.querySelectorAll('button')].find(b => b.textContent === 'Cancel').click()`);
                const gone = await waitFor(`!${formUnder(id)}${send ? ` && ${headOf(id)}.parentElement.querySelector(':scope > .notes').textContent.includes('Held under ${id}.')` : ''}`);
                await settled();
                const closed = await js(`${headOf(id)}.getBoundingClientRect().top`);
                steps.push({ label, isStuck, top, opened, gap, focus, closed, ok: shown && gone && focus && isStuck === stuck
                    && Math.abs(opened - top) < 1 && gap >= 0 && gap < 12 && Math.abs(closed - top) < 1 });
            }
            check(`${id}'s feedback and request forms open right under its header${stuck ? ', stuck' : ''}, which stays put as they open, send and cancel`,
                steps.every(x => x.ok), steps);
        }
    }

    // From the index, a drawn card scrolled above, its head stuck or gone,
    // comes into view with the form under its head, below every stuck head.
    for (const [name, place] of [['stuck', `(h => window.scrollBy(0, h.parentElement.getBoundingClientRect().top - parseFloat(getComputedStyle(h).top) + 250))(${headOf(`f${f4}`)})`],
        ['above', `window.scrollBy(0, ${q(`#f${f4}`)}.getBoundingClientRect().bottom + 50)`]]) {
        await load(`#c${c2}=f${f4},s${s22}~d,s${s23}~d&c${c3}=d`);
        await settled();
        await js(`document.body.classList.contains('tree-hidden') && ${q('#tree_open')}.click()`);
        await waitFor(`!!${q(`li[data-node="f${f4}"]`)}`);
        await js(place);
        const above = await js(`${q(`#f${f4}`)}.getBoundingClientRect().top < 0`);
        await js(`${q(`li[data-node="f${f4}"] > .node .widgets button[title="Add feedback for the agent"]`)}.click()`);
        const shown = await waitFor(`!!${formUnder(`f${f4}`)}`);
        await sleep(100);
        const seen = await js(`(f => {
            const r = f.getBoundingClientRect();
            const h = ${headOf(`f${f4}`)}.getBoundingClientRect();
            const hit = document.elementFromPoint(r.left + r.width / 2, r.top + 4);
            return { top: r.top, bottom: r.bottom, head: h.bottom, card: ${headOf(`f${f4}`)}.parentElement.getBoundingClientRect().top - h.top,
                header: ${q('#header')}.getBoundingClientRect().bottom, hit: f.contains(hit), focus: f.contains(document.activeElement) };
        })(${formUnder(`f${f4}`)})`);
        check(`a tree feedback button on a drawn card scrolled ${name} shows its form under its head, below the stuck heads`,
            above && shown && seen.top >= seen.head && seen.bottom <= await js('innerHeight') && Math.abs(seen.card) < 1 && seen.head > seen.header && seen.hit && seen.focus,
            { above, shown, ...seen });
    }

    // ---- download and close ----
    await js(`window.revoked = []; { const revoke = URL.revokeObjectURL.bind(URL); URL.revokeObjectURL = u => { window.revoked.push(u); revoke(u) } }`);
    await js(`[...document.querySelectorAll('#summary .tool')].find(b => b.textContent === 'download feedback').click()`);
    let file;
    for (let i = 0; i < 40 && !file; i++) {
        await sleep(100);
        file = fs.readdirSync(tmp).find(f => /^review-feedback-.*\.json$/.test(f));
    }
    check('download saves the export', file && JSON.parse(fs.readFileSync(path.join(tmp, file))).format === 'review-feedback', file);
    check('the download frees its object URL', await waitFor(`window.revoked.length === 1 && window.revoked[0].startsWith('blob:')`));
    await click('#close_review');
    check('close asks with the prune warning', await waitFor(`document.getElementById('title').textContent.includes('(closed)')`) && /prune/.test(dialogs.at(-1)), dialogs);
    check('close button hidden once closed', await js(`${q('#close_review')}.hidden`));

    // ---- side by side ----
    await load(step5);
    await click('#diff_toggle button[data-value="split"]');
    check('side by side: highlight marks only the new side', await waitFor(`(row => row && [...row.cells].map(c => c.classList.contains('hl') ? 1 : 0).join('') === '0011')(${q(`table.split[data-fid="${f4}"] tr[data-new="8"]`)})`));
    await click(`table.split[data-fid="${f4}"] tr[data-old="8"] td.ln[data-side="old"]`);
    check('side by side: old column opens an old-side box', await waitFor(`(f => f && f.querySelector('td').colSpan === 4 && f.querySelector('textarea').placeholder.includes('(old)'))(${q('tr.fb-form')})`));
    await shot('07-split');
    await click('#diff_toggle button[data-value="unified"]');

    // A changed file is blue, apart from plain links and from the ~changed
    // counts; hunk headers have their own color.
    const rgb = sel => js(`getComputedStyle(${q(sel)}).color.match(/\\d+/g).map(Number)`);
    const apart = (a, b) => a.reduce((n, x, i) => n + Math.abs(x - b[i]), 0) > 100;
    const changed = await rgb(`#c${c3} .file .toggle.changed`);
    const plain = await rgb(`#c${c7} .level:not(.card) .toggle`);
    const hunk = await rgb(`table[data-fid="${f4}"] tr.hunk td`);
    const nChg = await rgb(`#c${c3} .file .n-chg`);
    check('changed files are blue', changed.join() === '0,96,208', changed);
    check('changed files stand apart from plain links', apart(changed, plain), { changed, plain });
    check('the ~changed counts are not the changed file color', apart(nChg, changed), { nChg, changed });
    check('hunk headers are not the changed color', apart(hunk, changed), { hunk, changed });
    // ---- reading aloud ----
    // speechSynthesis is stubbed: speak records each utterance, which a
    // check ends by hand to move the reading on, and cancel counts.
    const voiceStub = list => `
        speechSynthesis.getVoices = () => ${JSON.stringify(list)};
        window.spoken = [];
        window.cancels = 0;
        speechSynthesis.speak = u => spoken.push(u);
        speechSynthesis.cancel = () => { cancels++ };
        window.SpeechSynthesisUtterance = class { constructor(text) { this.text = text } };
    `;
    const local = { name: 'Here', lang: 'en-US', localService: true, voiceURI: 'here' };
    const cloud = { name: 'Cloud', lang: 'en-US', localService: false, voiceURI: 'cloud' };
    let { identifier: speech } = await send('Page.addScriptToEvaluateOnNewDocument', { source: voiceStub([cloud, local]) });
    const said = () => js('spoken.map(u => u.text)');
    const lastSaid = () => js('spoken.at(-1)?.text ?? null');
    const endSaid = () => js('spoken.at(-1).onend()');
    // A block's buttons: its play button's glyph and title, and whether stop
    // is enabled.
    const speakState = sel => js(`(b => [b.children[0].querySelector('.pause-mark') ? '⏸' : b.children[0].textContent[0], b.children[0].title, !b.children[1].disabled])(${q(sel)})`);
    const whatSel = '#summary > :nth-child(1 of .prose-head) .speak';
    const whySel = '#summary > :nth-child(2 of .prose-head) .speak';
    const what = speechSentences(R.summary.what);
    const why = speechSentences(R.summary.why);
    await load();
    check('reading: nothing plays by itself', await js('spoken.length') === 0, await said());
    check('the voice menu picks the on-device voice by default', await js(`${q('#speech_voice')}.value`) === 'here');
    check('a block starts stopped', JSON.stringify(await speakState(whatSel)) === JSON.stringify(['▶', 'Read aloud', false]), await speakState(whatSel));
    await click(whatSel + ' .speak-play');
    check('play reads its first sentence, in the picked voice, and shows pause', await lastSaid() === what[0] && await js('spoken.at(-1).voice.voiceURI') === 'here'
        && JSON.stringify(await speakState(whatSel)) === JSON.stringify(['⏸', 'Pause reading', true]), [await said(), await speakState(whatSel)]);
    await endSaid();
    check('a sentence done, the next follows', await lastSaid() === what[1], await said());
    let n = await js('spoken.length');
    await click(whatSel + ' .speak-play');
    check('pause stops speech and keeps the place', await js(`cancels > 0 && spoken.length === ${n}`)
        && JSON.stringify(await speakState(whatSel)) === JSON.stringify(['▶', 'Resume reading', true]), await speakState(whatSel));
    await click(whatSel + ' .speak-play');
    check('play resumes from the paused sentence', await lastSaid() === what[1] && await js(`spoken.length`) === n + 1, await said());
    await click(whySel + ' .speak-play');
    check("another block's play stops this one, as a pause", await lastSaid() === why[0]
        && JSON.stringify(await speakState(whatSel)) === JSON.stringify(['▶', 'Resume reading', true])
        && JSON.stringify(await speakState(whySel)) === JSON.stringify(['⏸', 'Pause reading', true]), [await said(), await speakState(whatSel)]);
    await js(`(u => { window.stale = u })(spoken.at(-1))`);
    await click(whatSel + ' .speak-play');
    check('and its play resumes it where it was', await lastSaid() === what[1], await said());
    n = await js('spoken.length');
    await js('stale.onend()');
    check('an utterance cancelled meanwhile moves nothing on', await js('spoken.length') === n, await said());
    await js(`stale.onerror({ error: 'interrupted' })`);
    check('nor does its error pause the reading playing now', await js('spoken.length') === n
        && JSON.stringify(await speakState(whatSel)) === JSON.stringify(['⏸', 'Pause reading', true]), await speakState(whatSel));
    check('the pause button is two drawn bars, no emoji character', await js(`(b => b.textContent === '' && (m => !!m && m.offsetWidth > 0
        && getComputedStyle(m).borderLeftColor === getComputedStyle(b).color)(b.querySelector('.pause-mark')))(${q(whatSel + ' .speak-play')})`));
    check('the stop button is a drawn square in the text color, no emoji character', await js(`(b => b.textContent === '' && (m => !!m && m.offsetWidth > 0
        && getComputedStyle(m).backgroundColor === getComputedStyle(b).color)(b.querySelector('.stop-mark')))(${q(whatSel + ' .speak-stop')})`));
    await click(whatSel + ' .speak-stop');
    check('stop goes back to the start', JSON.stringify(await speakState(whatSel)) === JSON.stringify(['▶', 'Read aloud', false]), await speakState(whatSel));
    await click(whatSel + ' .speak-play');
    check('so play starts over', await lastSaid() === what[0], await said());
    for (let i = 1; i < what.length; i++) await endSaid();
    await endSaid();
    check('the whole block read, it is stopped again', await lastSaid() === what.at(-1) && JSON.stringify(await speakState(whatSel)) === JSON.stringify(['▶', 'Read aloud', false]),
        [await said(), await speakState(whatSel)]);
    await click('#summary > .speak-row .speak-play');
    check('the text read drops Markdown markers', await lastSaid() === 'Drops shout after feedback.', await said());
    await load(`#at=f${f7}&c${c3}=f${f7}`);
    await click(`#f${f7} > .prose-head .speak-play`);
    check('and reads :: as a pause', await lastSaid() === 'Drops the unused shout helper from Util, Shout, Helpers, For, Very, Loud, Greetings, And, More.', await said());
    n = await js('cancels');
    await click(`#f${f7} > .head`);
    check('collapsing the card being read pauses it at once', await waitFor(`!${q(`#f${f7}.card`)} && cancels > ${n}`), await js('cancels'));
    await click(`#f${f7} .toggle`);
    check('expanded again, it shows its place kept', await waitFor(`!!${q(`#f${f7} > .prose-head .speak`)}`)
        && JSON.stringify(await speakState(`#f${f7} > .prose-head .speak`)) === JSON.stringify(['▶', 'Resume reading', true]), await speakState(`#f${f7} > .prose-head .speak`));
    check('a commit card reads its what, why and how', await js(`document.querySelectorAll('#c${c3} > .card > .prose-head > .speak').length`) === 3);
    check('no buttons on short fields', await js(`!document.querySelector('.facts .speak, .meta .speak, .head .speak')`));
    const greetFlag = R.flags.find(f => f.id === flag1);
    await load(step5);
    await waitFor(`${q('#guide_count')}.textContent === '5 / 12'`);
    check('a guide step does not read itself while speech is off', await js('spoken.length') === 0, await said());
    await click('#guide_bar .speak-play');
    check("the bar's play reads the step", await lastSaid() === speechSentences(R.guide[4].commentary)[0], await said());
    await click('#speech_toggle');
    check('speech on reads the step from its start', await waitFor(`${q('#speech_toggle')}.getAttribute('aria-pressed') === 'true'`) && await lastSaid() === speechSentences(R.guide[4].commentary)[0], await said());
    await click('#guide_next');
    check('then each step as it shows', await waitFor(`${q('#guide_count')}.textContent === '6 / 12'`) && await lastSaid() === speechSentences(R.guide[5].commentary)[0], await said());
    await click(`.panel table.code-view[data-fid="${f4}"] .flag-icon[data-flag="${flag1}"]`);
    await waitFor(`!!${q('.flag-popup .speak')}`);
    await click('.flag-popup .speak-play');
    check("a flag popup's play reads its sentence, then its detail", await lastSaid() === greetFlag.sentence, await said());
    for (let i = 0; i < speechSentences(greetFlag.sentence).length; i++) await endSaid();
    check('its detail follows', await lastSaid() === speechSentences(greetFlag.detail)[0], await said());
    await click('#speech_menu > summary');
    await js(`(r => { r.value = '1.25'; r.dispatchEvent(new Event('input', { bubbles: true })); r.click() })(${q('#speech_rate')})`);
    check('a click in the speech menu leaves the popup and its reading', await js(`!!${q('.flag-popup')} && ${q('.flag-popup .speak-play')}.title === 'Pause reading'`));
    await endSaid();
    check('which takes the new speed from the next sentence', await js('spoken.at(-1).rate') === 1.25 && await lastSaid() === speechSentences(greetFlag.detail)[1], await said());
    await click('#speech_menu > summary');
    await js(`(r => { r.value = '1'; r.dispatchEvent(new Event('input', { bubbles: true })) })(${q('#speech_rate')})`);
    await js(`${q('.flag-popup .speak-play')}.focus(); [...${q('.flag-popup')}.querySelectorAll('button')].find(b => /^Mark (un)?seen$/.test(b.textContent)).click()`);
    check('a popup redrawn keeps the focus on its speech button', await waitFor(`document.activeElement?.matches('.flag-popup .speak-play') && ${q('.flag-popup')}.textContent.includes('Mark seen')`),
        await js('document.activeElement?.outerHTML.slice(0, 80)'));
    n = await js('cancels');
    await js(`document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))`);
    check('closing the popup pauses its reading at once', await waitFor(`!${q('.flag-popup')} && cancels > ${n}`), await js('cancels'));
    n = await js('spoken.length');
    await js('spoken.at(-1).onend()');
    check('and nothing more is read', await js('spoken.length') === n, await said());
    await click(`.panel table.code-view[data-fid="${f4}"] .flag-icon[data-flag="${flag1}"]`);
    check('reopened, it shows the reading paused, its place kept', await waitFor(`!!${q('.flag-popup .speak')}`)
        && JSON.stringify(await speakState('.flag-popup .speak')) === JSON.stringify(['▶', 'Resume reading', true]), await speakState('.flag-popup .speak'));
    await click('#summary h2');
    await click('#guide_exit');
    await waitFor(`${q('#guide_bar')}.hidden`);
    await click('#walk_new');
    await waitFor(`${param('w')} === 'f' && !${q('#guide_bar')}.hidden`);
    n = await js('spoken.length');
    check("the walker's speech is its own toggle, off", await js(`${q('#speech_toggle')}.getAttribute('aria-pressed') === 'false' && ${q('#speech_toggle')}.title.includes('flag')`) && await js('spoken.length') === n);
    await click('#speech_toggle');
    const walkNow = () => js(`fetch('api/review').then(r => r.json()).then(r => r.flags.find(f => f.id === Number(${param('g')})))`);
    const firstFlag = await walkNow();
    check('on, it reads the flag', await lastSaid() === speechSentences(firstFlag.sentence)[0], await said());
    await click('#guide_next');
    await waitFor(`${param('g')} !== '${firstFlag.id}'`);
    const second = await walkNow();
    check('and each flag as it arrives', await waitFor(`spoken.at(-1).text === ${JSON.stringify(speechSentences(second.sentence)[0])}`), await said());
    await click('#guide_exit');
    await load(step5);
    check("the guide's toggle kept its own state", await waitFor(`${q('#speech_toggle')}.getAttribute('aria-pressed') === 'true'`));
    // Outside the guide nothing reads by itself, toggles on or not.
    await js(`localStorage.setItem('review-helper.speech-walk', '1')`);
    await load(`#at=c${c2}`);
    await click(`li[data-node="f${f4}"] > .node > .link`);
    await waitFor(`!!${q(`#f${f4}.card`)}`);
    await click(cardFlag(`#f${f4}`, flagOf('Assumed nothing')) + ' > [data-flag]');
    await waitFor(`!!${q('.flag-popup')}`);
    await sleep(200);
    check('navigating and opening a flag outside the guide reads nothing', await js('spoken.length') === 0, await said());
    await click('#summary h2');
    await js(`localStorage.setItem('review-helper.speech', '0'); localStorage.setItem('review-helper.speech-walk', '0')`);
    await send('Page.removeScriptToEvaluateOnNewDocument', { identifier: speech });
    check('fixture: every flag unseen after the walk', await markAll(false));

    // Voices listed late, as Chrome does: a play pressed before waits for
    // them; one whose block has gone by then does not start.
    ({ identifier: speech } = await send('Page.addScriptToEvaluateOnNewDocument', { source: voiceStub([local]).replace('speechSynthesis.getVoices = () =>', 'window.lateVoices = ') + `
        speechSynthesis.getVoices = () => (window.voicesIn ? lateVoices : []);
    ` }));
    const voicesArrive = () => js(`window.voicesIn = true; speechSynthesis.dispatchEvent(new Event('voiceschanged'))`);
    // A gutter flag: its text has no card line, so its popup holds its only
    // buttons.
    await load(`#at=f${f4}&c${c2}=f${f4}~v`);
    await waitFor(`!!${q(`.panel table.code-view[data-fid="${f4}"] .flag-icon[data-flag="${flag1}"]`)}`);
    await click(`.panel table.code-view[data-fid="${f4}"] .flag-icon[data-flag="${flag1}"]`);
    await waitFor(`!!${q('.flag-popup .speak')}`);
    await click('.flag-popup .speak-play');
    await js(`document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))`);
    await waitFor(`!${q('.flag-popup')}`);
    // Back on the page, its wait is not: it ended when the block left.
    await click(`.panel table.code-view[data-fid="${f4}"] .flag-icon[data-flag="${flag1}"]`);
    await waitFor(`!!${q('.flag-popup .speak')}`);
    await voicesArrive();
    await sleep(200);
    check('voices arriving after the waiting block left start nothing, even with it back', await js('spoken.length') === 0 && await js(`${q('#speech_voice')}.value`) === 'here', await said());
    await click('#summary h2');
    await load();
    await click(whatSel + ' .speak-play');
    check('before the voices load, a play waits', await js('spoken.length') === 0 && await js(`${q('#speech_menu')}.open`), await said());
    await voicesArrive();
    check('they arrive: the waiting block plays', await waitFor(`spoken.length === 1`) && await lastSaid() === what[0], await said());
    await send('Page.removeScriptToEvaluateOnNewDocument', { identifier: speech });

    // The picked voice dropped by the browser: the default takes over; with
    // no voice left, the reading pauses.
    const there = { name: 'There', lang: 'en-GB', localService: true, voiceURI: 'there' };
    ({ identifier: speech } = await send('Page.addScriptToEvaluateOnNewDocument', { source: voiceStub([local, there]).replace('speechSynthesis.getVoices = () =>', 'window.liveVoices = ') + `
        speechSynthesis.getVoices = () => liveVoices;
    ` }));
    const voicesNow = list => js(`window.liveVoices = ${JSON.stringify(list)}; speechSynthesis.dispatchEvent(new Event('voiceschanged'))`);
    await load();
    await js(`(s => { s.value = 'there'; s.dispatchEvent(new Event('change')) })(${q('#speech_voice')})`);
    await click(whatSel + ' .speak-play');
    check('fixture: reading in the picked voice', await js('spoken.at(-1).voice.voiceURI') === 'there');
    await voicesNow([local]);
    await endSaid();
    check('the picked voice gone, the next sentence takes the default', await lastSaid() === what[1] && await js('spoken.at(-1).voice.voiceURI') === 'here', await said());
    await voicesNow([]);
    n = await js('spoken.length');
    await endSaid();
    check('with no voice left the reading pauses, its place kept', await js('spoken.length') === n
        && JSON.stringify(await speakState(whatSel)) === JSON.stringify(['▶', 'Resume reading', true]), await speakState(whatSel));
    await js(`localStorage.removeItem('review-helper.voice')`);
    await send('Page.removeScriptToEvaluateOnNewDocument', { identifier: speech });

    // Only online voices: never one by default, so playing asks for a voice
    // first.
    ({ identifier: speech } = await send('Page.addScriptToEvaluateOnNewDocument', { source: voiceStub([cloud]) }));
    await js(`localStorage.removeItem('review-helper.voice')`);
    await load(step5);
    await click('#speech_toggle');
    check('only online voices: speech on opens the menu at "Pick a voice" and says nothing yet', await waitFor(`${q('#speech_menu')}.open && ${q('#speech_voice')}.value === ''
        && ${q('#speech_voice')}.selectedOptions[0].textContent === 'Pick a voice'`) && await js('spoken.length') === 0, await said());
    await js(`(s => { s.value = 'cloud'; s.dispatchEvent(new Event('change')) })(${q('#speech_voice')})`);
    check('picking a voice starts speech', await lastSaid() === speechSentences(R.guide[4].commentary)[0], await said());
    await load(step5);
    check('the voice is remembered', await waitFor(`${q('#speech_voice')}.value === 'cloud' && spoken.at(-1)?.text === ${JSON.stringify(speechSentences(R.guide[4].commentary)[0])}`), await said());
    await js(`localStorage.setItem('review-helper.speech', '0')`);
    await send('Page.removeScriptToEvaluateOnNewDocument', { identifier: speech });

    // ---- a browser with no voices ----
    ({ identifier: speech } = await send('Page.addScriptToEvaluateOnNewDocument', { source: 'speechSynthesis.getVoices = () => []' }));
    await load(step5);
    check('without voices the speech toggle shows, disabled, with a hint, which the menu shows too', await waitFor(`!${q('#guide_bar')}.hidden && ${q('#speech_toggle')}.disabled
        && ${q('#speech_toggle')}.title.includes('speech-dispatcher') && !${q('#speech_hint')}.hidden && ${q('#speech_opts')}.hidden`));
    await click('#summary > .prose-head .speak-play');
    check('a play button opens the menu, which says why', await waitFor(`${q('#speech_menu')}.open && ${q('#speech_hint')}.checkVisibility()`));
    await send('Page.removeScriptToEvaluateOnNewDocument', { identifier: speech });

    // ---- phone width ----
    await send('Emulation.setDeviceMetricsOverride', { width: 390, height: 800, deviceScaleFactor: 1, mobile: true });
    await load(`#g=7&at=f${f5}&c${c2}=f${f5}~v`);
    await waitFor(`!!${q('tr.hl')}`);
    check('phone: index starts hidden', await js(`document.body.classList.contains('tree-hidden')`));
    check("phone: the guide bar's controls flow in wrapped rows under its text", await waitFor(`[...document.querySelectorAll('#guide_bar .crow:not([hidden])')].every(r => getComputedStyle(r).display === 'contents')
        && ${q('#guide_text')}.getBoundingClientRect().bottom <= ${q('#guide_back')}.getBoundingClientRect().top`));
    check('phone: the topic buttons show only their arrows, named in full', await waitFor(`[${q('#guide_prev_topic')}, ${q('#guide_next_topic')}].every(b => !b.hidden && b.getBoundingClientRect().width < 50)`)
        && await js(`${q('#guide_prev_topic')}.getAttribute('aria-label') === 'previous commit' && ${q('#guide_next_topic')}.getAttribute('aria-label') === 'next commit'`),
        await js(`[${q('#guide_prev_topic')}, ${q('#guide_next_topic')}].map(b => [b.hidden, b.getBoundingClientRect().width])`));
    // The walk pair is compact on a phone, so it shares a row with another
    // control and the fixed header stays short.
    const phoneHeader = await js(`(w => ({ height: ${q('#header')}.offsetHeight, shared: [...${q('#head_controls')}.querySelectorAll('.hgroup > *, :scope > *')].some(c => c !== w && !c.contains(w) && c.checkVisibility()
        && Math.abs(c.getBoundingClientRect().top - w.getBoundingClientRect().top) < 8) }))(${q('#walk_start')})`);
    check('phone: the walk pair shares a row and the header stays short', phoneHeader.shared && phoneHeader.height <= 200, phoneHeader);
    await js(`${q('#speech_menu')}.open = true`);
    check('phone: the speech menu opens on screen', await waitFor(`(r => r.width > 100 && r.left >= 0 && r.right <= innerWidth)(${q('#speech_menu > .menu')}.getBoundingClientRect())`),
        await js(`(r => [r.left, r.right, innerWidth])(${q('#speech_menu > .menu')}.getBoundingClientRect())`));
    await js(`${q('#speech_menu')}.open = false`);
    await click('#tree_open');
    check('phone: the tab opens the tree over the page, its close tab on its edge', await waitFor(`${onScreen('#tree')} && ${onScreen('#tree_close')}`)
        && await js(`Math.abs(${q('#tree_close')}.getBoundingClientRect().left - ${q('#tree')}.getBoundingClientRect().right) <= 2`), await treeState());
    await click('#tree_close');
    check('phone: the close tab closes it', await waitFor(`!${onScreen('#tree')} && ${onScreen('#tree_open')}`), await treeState());
    check('phone: the open tab is tall, centred, and clear of the cards and the guide bar', await tabFit('#tree_open') === true, await tabFit('#tree_open'));
    const mainLeft = () => js(`${q('#main')}.getBoundingClientRect().left`);
    const closedLeft = await mainLeft();
    await click('#tree_open');
    await waitFor(onScreen('#tree'));
    check('phone: opening the tree does not shift the page', await mainLeft() === closedLeft, [closedLeft, await mainLeft()]);
    await click('#tree_close');
    await waitFor(`!${onScreen('#tree')}`);
    check('phone: no horizontal page scroll', await js('document.documentElement.scrollWidth <= 390'), await js('document.documentElement.scrollWidth'));
    await shot('06-phone-guide');
    // A comment badge on an open head stays in sight beside the name, the
    // head clipping its row's end instead.
    const badgeShown = id => `(b => { if (!b) return 'none';
        const r = b.getBoundingClientRect(), w = b.parentElement.getBoundingClientRect();
        return (r.width > 0 && r.left >= w.left - 0.5 && r.right <= w.right + 0.5 && b.contains(document.elementFromPoint((r.left + r.right) / 2, (r.top + r.bottom) / 2))) || [r.left, r.right, w.left, w.right];
    })(${q(`#${id} > .card > .head > .row > .cmt-badge, #${id}.card > .head > .row > .cmt-badge`)})`;
    for (const [id, hash] of [[`f${f4}`, `#at=f${f4}&c${c2}=f${f4}~v`], [`s${s22}`, `#at=s${s22}&c${c2}=f${f4},s${s22}~v`], [`c${c2}`, `#at=c${c2}&c${c2}=d`]]) {
        await load(hash);
        check(`phone: the comment badge on the head of ${id[0] === 'f' ? 'a file' : id[0] === 's' ? 'a section' : 'a commit'} shows`, await waitFor(`${badgeShown(id)} === true`), await js(badgeShown(id)));
    }
    // Open heads keep their names readable beside their tabs and links.
    await load(`#c${c2}=f${f4},s${s22}~d`);
    await waitFor(`!!${q(`#s${s22}.card > .head`)}`);
    const headNames = await js(`[${q(`#c${c2} > .card > .head`)}, ${q(`#f${f4} > .head`)}, ${q(`#s${s22} > .head`)}]
        .map(h => Math.round(h.querySelector('.title').getBoundingClientRect().width))`);
    check('phone: open commit, file and section heads show their names', headNames.every(w => w >= 60), headNames);
    check('phone: their tabs and links stay inside the head', await js(`[${q(`#c${c2} > .card > .head`)}, ${q(`#f${f4} > .head`)}, ${q(`#s${s22} > .head`)}]
        .every(h => [...h.querySelectorAll(':scope > .tabs, :scope > .widgets')].every(w => w.getBoundingClientRect().right <= h.getBoundingClientRect().right + 1))`));
    await js(`${q(`#s${s22}`)}.scrollIntoView()`);
    await shot('07-phone-heads');
    // Long URLs and identifiers wrap: a commit message trailer, agent prose.
    // innerWidth grows with the page under mobile emulation, so the width is
    // compared with the device's.
    await load();
    await js(`document.querySelectorAll('#c${c3} details').forEach(d => { d.open = true })`);
    check('phone: a long commit message token wraps', await js(`${q(`#c${c3} pre.message`)}.textContent.includes('session_') && document.documentElement.scrollWidth <= 390`),
        await js('document.documentElement.scrollWidth'));
    await load(`#at=f${f7}&c${c3}=f${f7}`);
    check('phone: a long code span in prose wraps', await js(`${q(`#f${f7}`)}.textContent.includes('Very::Loud') && document.documentElement.scrollWidth <= 390`),
        await js('document.documentElement.scrollWidth'));

    check('no stray null text', !(await js(`document.body.innerText.includes('null')`)));
    check('no runtime errors', errors.length === 0, errors);
}
catch (err) {
    check('driver ran', false, String(err.stack || err));
}
ws?.close();
console.log(`1..${count}`);
await finish(failed ? 1 : 0);
