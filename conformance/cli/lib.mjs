// Shared helpers for the CLI conformance tests: run the review-helper
// dispatcher (bin/review-helper, so REVIEW_HELPER_IMPL picks the backend),
// build scratch git repositories with plain git, fill sheets, and talk to a
// served review. conformance/README.md, "The CLI checks", says how to run them.
import { spawn, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
export const DISPATCHER = path.resolve(here, '../../bin/review-helper');

// The time any one check may take, for node:test's per-test timeout.
export const TIMEOUT = 120000;

// Scratch directories and servers still running are cleaned up on exit,
// and on an interrupt.
const scratch = [];
const servers = new Set();
function cleanUp() {
    for (const child of servers) killGroup(child, 'SIGKILL');
    for (const d of scratch) fs.rmSync(d, { recursive: true, force: true });
}
process.on('exit', cleanUp);
for (const signal of ['SIGINT', 'SIGTERM', 'SIGHUP']) process.once(signal, () => { cleanUp(); process.exit(1) });

const ROOT = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), 'rh-cli-')));
scratch.push(ROOT);

export function tempDir() {
    return fs.mkdtempSync(path.join(ROOT, 'd-'));
}

// Git sees only what the checks set: no user or system config, no hooks or
// signing, no repository variables inherited from a caller (a git hook that
// runs the checks, say), and no repository above the scratch root.
const GIT_CONFIG = path.join(ROOT, 'gitconfig');
fs.writeFileSync(GIT_CONFIG, [
    '[user]', '\tname = Conformance', '\temail = conformance@example.com',
    '[commit]', '\tgpgsign = false', '[tag]', '\tgpgsign = false',
    '[core]', '\thooksPath = /dev/null', '[init]', '\tdefaultBranch = main', '',
].join('\n'));

// The environment every command runs in. Serve settings an implementation
// reads every time (CLI.md, "Optional options with standard names") are
// cleared, so the checks set up their own conditions; extra adds to it.
export function env(extra = {}) {
    const e = { ...process.env };
    for (const k of Object.keys(e)) {
        if (k.startsWith('GIT_') || (k.startsWith('REVIEW_HELPER_') && k !== 'REVIEW_HELPER_IMPL')) delete e[k];
    }
    return {
        ...e,
        GIT_CONFIG_GLOBAL: GIT_CONFIG, GIT_CONFIG_NOSYSTEM: '1', GIT_CEILING_DIRECTORIES: ROOT,
        GIT_AUTHOR_NAME: 'Conformance', GIT_AUTHOR_EMAIL: 'conformance@example.com',
        GIT_COMMITTER_NAME: 'Conformance', GIT_COMMITTER_EMAIL: 'conformance@example.com',
        ...extra,
    };
}

export function git(dir, ...args) {
    const r = spawnSync('git', args, { cwd: dir, encoding: 'utf8', env: env() });
    if (r.status !== 0) throw new Error(`git ${args.join(' ')} failed: ${r.stderr}`);
    return r.stdout.trim();
}

// A repository with one base commit; returns its directory.
export function makeRepo(files = { 'README': 'Scratch.\n' }) {
    const dir = tempDir();
    git(dir, 'init', '-q', '-b', 'main');
    git(dir, 'config', 'user.name', 'Conformance');
    git(dir, 'config', 'user.email', 'conformance@example.com');
    commit(dir, 'Base', files);
    return dir;
}

// Write (or, for null, delete) files and commit them; returns the revision.
export function commit(dir, message, files) {
    for (const [name, content] of Object.entries(files)) {
        const file = path.join(dir, name);
        if (content === null) fs.rmSync(file);
        else {
            fs.mkdirSync(path.dirname(file), { recursive: true });
            fs.writeFileSync(file, content);
        }
    }
    git(dir, 'add', '-A');
    git(dir, 'commit', '-q', '-m', message);
    return git(dir, 'rev-parse', 'HEAD');
}

// Run the review-helper command in dir: { status, stdout, stderr }.
export function rh(dir, args, input) {
    const r = spawnSync('sh', [DISPATCHER, ...args], { cwd: dir, encoding: 'utf8', input, env: env() });
    return { status: r.status, stdout: r.stdout, stderr: r.stderr };
}

// Run it and require success; returns stdout.
export function rhOk(dir, args, input) {
    const r = rh(dir, args, input);
    if (r.status !== 0) throw new Error(`review-helper ${args.join(' ')} exited ${r.status}: ${r.stderr}`);
    return r.stdout;
}

export const json = (dir, args) => JSON.parse(rhOk(dir, args));

// The journal file of the worktree at dir.
export const journalFile = dir => path.resolve(dir, git(dir, 'rev-parse', '--git-path', 'review-helper'), 'journal.jsonl');

export function journal(dir) {
    const file = journalFile(dir);
    if (!fs.existsSync(file)) return [];
    return fs.readFileSync(file, 'utf8').split('\n').filter(l => l.length).map(l => JSON.parse(l));
}

// Write a sheet to a file and fill it: the command's result.
export function fill(dir, file, sheet) {
    const sheetFile = path.join(tempDir(), 'sheet.json');
    fs.writeFileSync(sheetFile, JSON.stringify(sheet));
    return rh(dir, ['fill', file, sheetFile]);
}

// Text for every required field of an item, or null to leave it out.
export function fillFields(item) {
    switch (item.type) {
        case 'summary': return { what: 'What it does.', why: 'Why.', how: 'How.' };
        case 'commit': return { what: `Commit ${item.about.position}.`, why: 'Why.', how: 'How.' };
        case 'order': return { paths: item.about.paths };
        case 'file': return { what: `The file ${item.about.path}.` };
        case 'section': return { what: 'A section.' };
        case 'split': return { sections: [] };
        case 'topics': return { topics: [{ name: 'Everything', paths: item.about.paths }] };
        case 'guide': return { steps: [{ target: { type: 'summary' }, highlight: [], commentary: 'Start with the summary.' }] };
        default: return null;
    }
}

// The sheet with the fields pick(item) gives each item; an item it gives
// none is dropped.
export function filled(sheet, pick = fillFields) {
    const items = [];
    for (const item of sheet.items) {
        const fields = pick(item);
        if (fields) items.push({ ...item, fields: { ...item.fields, ...fields } });
    }
    return { ...sheet, items };
}

// Signal a server and everything it started: it runs in a process group of
// its own, so a launcher that does not exec its server is no obstacle.
function killGroup(child, signal) {
    try { process.kill(-child.pid, signal) } catch { /* already gone */ }
}

// Serve a review (args after the dispatcher, as ['serve', FILE] or [FILE]);
// resolves to { url, stop } once the first line, the URL, is printed.
export function serve(dir, args, extraEnv = {}) {
    return new Promise((resolve, reject) => {
        const child = spawn('sh', [DISPATCHER, ...args], { cwd: dir, env: env(extraEnv), stdio: ['ignore', 'pipe', 'pipe'], detached: true });
        servers.add(child);
        const gone = new Promise(r => child.once('exit', () => { servers.delete(child); r() }));
        const stop = async () => {
            if (child.exitCode !== null || child.signalCode !== null) return;
            killGroup(child, 'SIGTERM');
            const timer = setTimeout(() => killGroup(child, 'SIGKILL'), 5000);
            await gone;
            clearTimeout(timer);
        };
        let out = '', err = '';
        const timer = setTimeout(() => { stop(); reject(new Error(`serve printed no URL: ${err}`)) }, 30000);
        child.stderr.on('data', d => { err += d });
        child.stdout.on('data', d => {
            out += d;
            const nl = out.indexOf('\n');
            if (nl < 0) return;
            clearTimeout(timer);
            resolve({ url: out.slice(0, nl), stop });
        });
        gone.then(() => { clearTimeout(timer); reject(new Error(`serve exited before printing a URL: ${err}`)) });
    });
}

export async function post(url, route, body) {
    const res = await fetch(new URL(route, url), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    const data = await res.json();
    if (!res.ok) throw new Error(`POST ${route}: ${res.status} ${JSON.stringify(data)}`);
    return data;
}
