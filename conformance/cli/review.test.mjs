// build, sheet, fill, iterate (carrying text and flags), list, close, prune.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { TIMEOUT, rh, rhOk, json, makeRepo, commit, git, journal, fill, filled, fillFields } from './lib.mjs';

const SIX = 'one\ntwo\nthree\nfour\nfive\nsix\n';

// A repository with two reviewed commits, one adding notes.txt and one
// six.txt, three flag notes on six.txt and one journal entry; the review is
// built.
function reviewed() {
    const dir = makeRepo();
    const base = git(dir, 'rev-parse', 'HEAD');
    const first = commit(dir, 'Add notes', { 'notes.txt': 'A note.\n' });
    const head = commit(dir, 'Add six lines', { 'six.txt': SIX });
    const flags = ['Keep me.', 'Make me red.', 'Retire me.'].map(sentence =>
        rhOk(dir, ['note', '--flag', 'yellow', '--kind', 'note', '--path', 'six.txt', '--sentence', sentence]).trim());
    const entry = rhOk(dir, ['note', '--path', 'notes.txt', '--sentence', 'Just so you know.']).trim();
    const out = rhOk(dir, ['build', '--base', base, '--title', 'Six lines']);
    assert.match(out, /^[^\n]+\n$/, 'build prints one line');
    return { dir, base, first, head, flags, entry, file: out.trim() };
}

// Fill every item, placing each flag note on its own line of six.txt.
function fillAll(r, sheet) {
    const lines = Object.fromEntries(r.flags.map((id, i) => [`note:${id}`, [2 * i + 2, 2 * i + 2]]));
    return fill(r.dir, r.file, filled(sheet, item => item.type === 'note'
        ? { commit: r.head, path: 'six.txt', lines: lines[item.id] }
        : fillFields(item)));
}

test('build makes a review of the history, and its sheet asks for everything', { timeout: TIMEOUT }, () => {
    const r = reviewed();
    assert.ok(path.isAbsolute(r.file) && fs.statSync(r.file).isFile(), 'the absolute path of the review file');

    const list = rhOk(r.dir, ['list']).split('\n').filter(Boolean).map(l => l.split('\t'));
    assert.equal(list.length, 1);
    assert.deepEqual(list[0].slice(1), ['open', r.file], 'registered open');

    const notes = journal(r.dir);
    assert.ok(!notes.some(n => n.id === r.entry), 'the journal entry went into the iteration');
    assert.deepEqual(notes.map(n => n.id).sort(), [...r.flags].sort(), 'flag notes wait for placement');

    const sheet = json(r.dir, ['sheet', r.file]);
    assert.equal(sheet.format, 'review-sheet');
    assert.equal(sheet.format_version, 1);
    assert.equal(sheet.review, list[0][0]);
    assert.equal(sheet.iteration, 1);
    const ids = sheet.items.map(i => i.id);
    for (const id of ['summary:1', 'guide:1', ...r.flags.map(f => `note:${f}`)]) assert.ok(ids.includes(id), `${id} is asked for`);
    const commits = sheet.items.filter(i => i.type === 'commit');
    assert.deepEqual(commits.sort((a, b) => a.about.position - b.about.position).map(c => c.about.revision), [r.first, r.head]);
    for (const item of sheet.items) {
        assert.equal(typeof item.rev, 'string');
        assert.ok(Object.values(item.fields).every(v => v === null), `${item.id} starts empty`);
    }
    for (const n of sheet.items.filter(i => i.type === 'note')) assert.equal(n.optional, true, 'notes are optional');
});

test('build needs a base unless squashed; squashed, the base is the empty revision', { timeout: TIMEOUT }, () => {
    const dir = makeRepo();
    const r = rh(dir, ['build']);
    assert.notEqual(r.status, 0);
    assert.notEqual(r.stderr, '');

    const file = rhOk(dir, ['build', '--squash']).trim();
    const summary = json(dir, ['sheet', file]).items.find(i => i.type === 'summary');
    // Agent Note: git's empty revision is the empty tree.
    assert.equal(summary.about.base, git(dir, 'hash-object', '-t', 'tree', '/dev/null'));
    assert.equal(summary.about.head, git(dir, 'rev-parse', 'HEAD'));
});

test('fill writes a whole sheet, and sheet --all gives it back', { timeout: TIMEOUT }, () => {
    const r = reviewed();
    const sheet = json(r.dir, ['sheet', r.file]);
    const res = fillAll(r, sheet);
    assert.equal(res.status, 0, res.stderr);
    assert.match(res.stdout, /^filled \d+, remaining 0\n$/);
    assert.deepEqual(journal(r.dir), [], 'placed notes leave the journal');

    assert.deepEqual(json(r.dir, ['sheet', r.file]).items, [], 'nothing left to do');
    const all = json(r.dir, ['sheet', '--all', r.file]);
    const summary = all.items.find(i => i.id === 'summary:1');
    assert.deepEqual([summary.fields.what, summary.fields.why, summary.fields.how], ['What it does.', 'Why.', 'How.']);
    const flags = all.items.filter(i => i.type === 'flag');
    assert.deepEqual(flags.map(f => f.about.lines).sort(), [[2, 2], [4, 4], [6, 6]]);
    assert.ok(flags.every(f => f.about.color === 'yellow' && f.about.pending === false));
    assert.ok(!all.items.some(i => i.type === 'note'), 'placed notes are flags now');

    // A rewrite through --all replaces the text.
    const again = { ...all, items: [{ ...summary, fields: { ...summary.fields, what: 'Better.' } }] };
    assert.equal(fill(r.dir, r.file, again).status, 0);
    assert.equal(json(r.dir, ['sheet', '--all', r.file]).items.find(i => i.id === 'summary:1').fields.what, 'Better.');
    // The same item again is stale: its rev changed with the rewrite.
    const stale = fill(r.dir, r.file, again);
    assert.notEqual(stale.status, 0);
    assert.match(stale.stderr, /summary:1/);
});

test('fill takes a sheet on standard input', { timeout: TIMEOUT }, () => {
    const r = reviewed();
    const sheet = json(r.dir, ['sheet', r.file]);
    const one = { ...sheet, items: sheet.items.filter(i => i.id === 'summary:1').map(i => ({ ...i, fields: fillFields(i) })) };
    const out = rhOk(r.dir, ['fill', r.file, '-'], JSON.stringify(one));
    assert.match(out, /^filled 1, remaining [1-9]\d*\n$/);
});

test('fill rejects a bad sheet whole, naming the item', { timeout: TIMEOUT }, () => {
    const r = reviewed();
    const sheet = json(r.dir, ['sheet', r.file]);
    const good = sheet.items.find(i => i.id === 'summary:1');
    const order = sheet.items.find(i => i.type === 'order');
    const split = sheet.items.find(i => i.type === 'split' && i.about.path === 'six.txt');
    const guide = sheet.items.find(i => i.type === 'guide');
    const bad = [
        ['an unknown id', { id: 'commit:999999', type: 'commit', rev: 'x', about: {}, fields: { what: 'a', why: 'b', how: 'c' } }, /commit:999999/],
        ['a stale rev', { ...good, rev: 'not-the-rev', fields: fillFields(good) }, /summary:1/],
        ['an empty string', { ...good, fields: { what: '', why: 'b', how: 'c' } }, /summary:1/],
        ['an order missing a path', { ...order, fields: { paths: order.about.paths.slice(1) } }, new RegExp(order.id)],
        ['a section outside the file', { ...split, fields: { sections: [{ name: 'all', package: null, lines: [[1, 60]], first_line: 'one' }] } }, new RegExp(split.id)],
        ['a diff step naming a section but no path', {
            ...guide, fields: { steps: [{ target: { type: 'diff', commit: r.head, section: { name: 'all', package: null } }, highlight: [], commentary: 'Here.' }] },
        }, new RegExp(guide.id)],
    ];
    for (const [what, item, names] of bad) {
        const items = what === 'a stale rev' ? [item] : [{ ...good, fields: fillFields(good) }, item];
        const res = fill(r.dir, r.file, { ...sheet, items });
        assert.notEqual(res.status, 0, `${what} is rejected`);
        assert.equal(res.stdout, '');
        assert.match(res.stderr, names, `the error names the item (${what})`);
    }
    assert.deepEqual(json(r.dir, ['sheet', r.file]), sheet, 'nothing was written');

    const notJson = rh(r.dir, ['fill', r.file, '-'], '{ not json');
    assert.notEqual(notJson.status, 0);
    assert.notEqual(notJson.stderr, '');
});

test('iterate carries the text of unchanged commits', { timeout: TIMEOUT }, () => {
    const r = reviewed();
    assert.equal(fillAll(r, json(r.dir, ['sheet', r.file])).status, 0);
    // Rewrite the second commit, as a fixup after review does.
    fs.writeFileSync(`${r.dir}/six.txt`, 'one\nTWO\nthree\nFOUR\nfive\nSIX\n');
    git(r.dir, 'commit', '-q', '-a', '--amend', '--no-edit');
    const next = git(r.dir, 'rev-parse', 'HEAD');
    rhOk(r.dir, ['note', '--path', 'six.txt', '--sentence', 'Changed three lines.']);

    assert.equal(rhOk(r.dir, ['iterate', r.file]), '2\n');
    assert.deepEqual(journal(r.dir), [], 'the new entry went into iteration 2');

    const sheet = json(r.dir, ['sheet', r.file]);
    assert.equal(sheet.iteration, 2);
    const commits = sheet.items.filter(i => i.type === 'commit').map(i => i.about.revision);
    assert.deepEqual(commits, [next], 'only the rewritten commit needs text');
    const all = json(r.dir, ['sheet', '--all', r.file]);
    const kept = all.items.find(i => i.type === 'commit' && i.about.revision === r.first);
    assert.equal(kept.fields.what, 'Commit 1.', "the unchanged commit's text carried");
});

test('a rewritten commit keeps its flags, pending on the commit that replaced it', { timeout: TIMEOUT }, () => {
    const r = reviewed();
    assert.equal(fillAll(r, json(r.dir, ['sheet', r.file])).status, 0);
    // Amend the flagged commit: a new line on top moves every flagged line down.
    fs.writeFileSync(`${r.dir}/six.txt`, `zero\n${SIX}`);
    git(r.dir, 'commit', '-q', '-a', '--amend', '--no-edit');
    const next = git(r.dir, 'rev-parse', 'HEAD');
    assert.equal(rhOk(r.dir, ['iterate', r.file]), '2\n');

    const sheet = json(r.dir, ['sheet', r.file]);
    const flags = sheet.items.filter(i => i.type === 'flag');
    assert.equal(flags.length, 3, 'every flag of the rewritten commit is asked about');
    for (const f of flags) {
        assert.equal(f.about.pending, true);
        assert.equal(f.about.rewritten, true);
        assert.equal(f.about.commit, next, 'on the commit that replaced it');
        assert.equal(f.about.path, 'six.txt');
        assert.equal(f.about.previous.commit, r.head);
        assert.deepEqual(f.about.lines, f.about.previous.lines.map(n => n + 1), 'its lines moved with the code');
    }

    const kept = fill(r.dir, r.file, { ...sheet, items: flags.map(f => ({ ...f, fields: { decision: 'keep', color: null, reason: null } })) });
    assert.equal(kept.status, 0, kept.stderr);
    const after = json(r.dir, ['sheet', '--all', r.file]).items.filter(i => i.type === 'flag');
    assert.deepEqual(after.map(f => [f.about.color, f.about.pending, f.about.commit]), flags.map(() => ['yellow', false, next]));

    // Nothing rewritten since: the kept flags carry settled, once each.
    git(r.dir, 'commit', '-q', '--allow-empty', '-m', 'Nothing changed');
    assert.equal(rhOk(r.dir, ['iterate', r.file]), '3\n');
    assert.ok(!json(r.dir, ['sheet', r.file]).items.some(i => i.type === 'flag'), 'no flag is asked about again');
    const third = json(r.dir, ['sheet', '--all', r.file]).items.filter(i => i.type === 'flag');
    assert.deepEqual(third.map(f => f.about.note).sort(), flags.map(f => f.about.note).sort());
    assert.ok(third.every(f => f.about.pending === false && f.about.commit === next));
});

test("a rewritten commit's flags follow a file it renamed", { timeout: TIMEOUT }, () => {
    const r = reviewed();
    assert.equal(fillAll(r, json(r.dir, ['sheet', r.file])).status, 0);
    // Amend the flagged commit to add the file under another name.
    git(r.dir, 'mv', 'six.txt', 'seven.txt');
    git(r.dir, 'commit', '-q', '--amend', '--no-edit');
    const next = git(r.dir, 'rev-parse', 'HEAD');
    assert.equal(rhOk(r.dir, ['iterate', r.file]), '2\n');

    const flags = json(r.dir, ['sheet', r.file]).items.filter(i => i.type === 'flag');
    assert.equal(flags.length, 3, 'every flag of the rewritten commit is asked about');
    for (const f of flags) {
        assert.equal(f.about.rewritten, true);
        assert.equal(f.about.commit, next, 'on the commit that replaced it');
        assert.equal(f.about.path, 'seven.txt', 'on the file under its new name');
        assert.deepEqual(f.about.lines, f.about.previous.lines, 'on the same lines');
    }
});

test('a flag kept on a file outside the change stays settled through a rebase', { timeout: TIMEOUT }, () => {
    const dir = makeRepo();
    const base = git(dir, 'rev-parse', 'HEAD');
    const head = commit(dir, 'Add six lines', { 'six.txt': SIX });
    const gone = commit(dir, 'Add a file', { 'gone.txt': 'gone\n' });
    const note = rhOk(dir, ['note', '--flag', 'yellow', '--path', 'gone.txt', '--sentence', 'About the file.']).trim();
    const file = rhOk(dir, ['build', '--base', base]).trim();
    assert.equal(fill(dir, file, filled(json(dir, ['sheet', file]), item => item.type === 'note'
        ? { commit: gone, path: 'gone.txt', lines: null } : fillFields(item))).status, 0);

    // Drop the commit adding the file: its flag goes pending, its file out
    // of the change; the agent keeps it.
    git(dir, 'reset', '-q', '--hard', head);
    assert.equal(rhOk(dir, ['iterate', file]), '2\n');
    const pending = json(dir, ['sheet', file]);
    const flag = pending.items.find(i => i.type === 'flag' && i.about.note === note);
    assert.equal(flag.about.left_change, true);
    assert.equal(fill(dir, file, { ...pending, items: [{ ...flag, fields: { decision: 'keep', color: null, reason: null } }] }).status, 0);

    // Rebase onto a new base: every commit gets a new revision.
    git(dir, 'checkout', '-q', '-b', 'newbase', base);
    const newBase = commit(dir, 'Something else', { 'other.txt': 'other\n' });
    git(dir, 'checkout', '-q', 'main');
    git(dir, 'rebase', '-q', newBase);
    assert.equal(rhOk(dir, ['iterate', file, '--base', newBase]), '3\n');
    const again = json(dir, ['sheet', '--all', file]).items.find(i => i.type === 'flag' && i.about.note === note);
    assert.equal(again.about.pending, false, 'not asked about again');
    assert.equal(again.about.path, 'gone.txt');
});

test('a squashed review asks about flags whose code changed: keep, recolor, retire', { timeout: TIMEOUT }, () => {
    const dir = makeRepo();
    const base = git(dir, 'rev-parse', 'HEAD');
    const head = commit(dir, 'Add six lines', { 'six.txt': SIX });
    const notes = ['Keep me.', 'Make me red.', 'Retire me.'].map(sentence =>
        rhOk(dir, ['note', '--flag', 'yellow', '--path', 'six.txt', '--sentence', sentence]).trim());
    const file = rhOk(dir, ['build', '--squash', '--base', base]).trim();

    // Topics first: a sheet that fills them may hold nothing that names an entry.
    let sheet = json(dir, ['sheet', file]);
    assert.equal(sheet.items.find(i => i.type === 'summary').about.squashed, true);
    const topics = sheet.items.find(i => i.type === 'topics');
    assert.ok(topics, 'a squashed review asks for topics');
    assert.equal(fill(dir, file, filled({ ...sheet, items: [topics] })).status, 0);
    sheet = json(dir, ['sheet', file]);
    const lines = Object.fromEntries(notes.map((id, i) => [`note:${id}`, [2 * i + 2, 2 * i + 2]]));
    const res = fill(dir, file, filled(sheet, item => item.type === 'note'
        ? { commit: head, topic: null, path: 'six.txt', lines: lines[item.id] } : fillFields(item)));
    assert.equal(res.status, 0, res.stderr);

    commit(dir, 'Change the flagged lines', { 'six.txt': 'one\nTWO\nthree\nFOUR\nfive\nSIX\n' });
    assert.equal(rhOk(dir, ['iterate', file]), '2\n');
    sheet = json(dir, ['sheet', file]);
    const flags = sheet.items.filter(i => i.type === 'flag');
    assert.equal(flags.length, 3, 'each flag on a changed line is asked about');
    assert.ok(flags.every(f => f.about.pending === true && f.about.previous !== null));
    const by = s => flags.find(f => f.about.sentence === s);

    const noReason = { ...sheet, items: [{ ...by('Make me red.'), fields: { decision: 'recolor', color: 'red', reason: null } }] };
    assert.notEqual(fill(dir, file, noReason).status, 0, 'a recolor needs a reason');

    const decided = { ...sheet, items: [
        { ...by('Keep me.'), fields: { decision: 'keep', color: null, reason: null } },
        { ...by('Make me red.'), fields: { decision: 'recolor', color: 'red', reason: 'It got worse.' } },
        { ...by('Retire me.'), fields: { decision: 'retire', color: null, reason: null } },
    ] };
    const done = fill(dir, file, decided);
    assert.equal(done.status, 0, done.stderr);

    const after = json(dir, ['sheet', '--all', file]).items.filter(i => i.type === 'flag');
    assert.deepEqual(after.map(f => [f.about.sentence, f.about.color, f.about.pending]).sort(),
        [['Keep me.', 'yellow', false], ['Make me red.', 'red', false]]);
    assert.equal(after.find(f => f.about.sentence === 'Make me red.').about.color_reason, 'It got worse.');
});

test('close, then prune removes its pins; a dry run only lists them', { timeout: TIMEOUT }, () => {
    const r = reviewed();
    const id = rhOk(r.dir, ['list']).split('\t')[0];
    assert.equal(rhOk(r.dir, ['prune', '--dry-run']), '', 'an open review keeps its pins');

    assert.equal(rhOk(r.dir, ['close', r.file]), '');
    assert.deepEqual(rhOk(r.dir, ['list']).trim().split('\t'), [id, 'closed', r.file]);

    const pins = rhOk(r.dir, ['prune', '--dry-run']).split('\n').filter(Boolean);
    assert.equal(pins.length, 1, 'one iteration, one pin');
    // Agent Note: pins are VCS names; for git they are refs.
    const refs = () => git(r.dir, 'for-each-ref', '--format=%(refname)').split('\n');
    assert.ok(refs().includes(pins[0]), 'the dry run removed nothing');
    assert.deepEqual(rhOk(r.dir, ['prune']).split('\n').filter(Boolean), pins);
    assert.ok(!refs().includes(pins[0]), 'prune removed it');
    assert.equal(rhOk(r.dir, ['prune', '--dry-run']), '');
});
