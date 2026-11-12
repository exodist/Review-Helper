// export and import: sharing a review with another copy of the repository.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { TIMEOUT, rh, rhOk, json, makeRepo, commit, git, tempDir, fill, filled } from './lib.mjs';

function review() {
    const dir = makeRepo();
    const base = git(dir, 'rev-parse', 'HEAD');
    commit(dir, 'Add a file', { 'a.txt': 'a\n' });
    const file = rhOk(dir, ['build', '--base', base, '--title', 'Shared']).trim();
    assert.equal(fill(dir, file, filled(json(dir, ['sheet', file]))).status, 0);
    return { dir, file, id: rhOk(dir, ['list']).split('\t')[0] };
}

const cloneOf = dir => {
    const clone = path.join(tempDir(), 'clone');
    git(path.dirname(clone), 'clone', '-q', dir, clone);
    return clone;
};

test('export prints the review as JSON, or writes it with --out', { timeout: TIMEOUT }, () => {
    const r = review();
    const text = rhOk(r.dir, ['export', r.file]);
    const data = JSON.parse(text);
    assert.equal(data.format, 'review-export');
    assert.equal(data.format_version, 1);
    assert.equal(typeof data.generator, 'string');
    for (const t of ['feedback', 'request', 'flag_seen']) assert.ok(!(t in data.tables), `no ${t} table`);
    assert.equal(data.tables.review.rows.length, 1);
    for (const table of Object.values(data.tables)) {
        assert.ok(Array.isArray(table.columns) && Array.isArray(table.rows));
        assert.ok(table.rows.every(row => row.length === table.columns.length));
    }

    const out = path.join(tempDir(), 'new', 'dir', 'shared.json');
    assert.equal(rhOk(r.dir, ['export', r.file, '--out', out]), '', 'nothing on standard output with --out');
    assert.deepEqual(JSON.parse(fs.readFileSync(out, 'utf8')), data, 'missing directories are made');
});

test('import makes an open review in another copy, and merges when it is there', { timeout: TIMEOUT }, () => {
    const r = review();
    const clone = cloneOf(r.dir);
    assert.equal(rhOk(r.dir, ['close', r.file]), '');
    const exported = rhOk(r.dir, ['export', r.file]);

    const res = rh(clone, ['import', '-'], exported);
    assert.equal(res.status, 0, res.stderr);
    assert.equal(res.stderr, '', 'every head is present');
    const file = res.stdout.trim();
    assert.ok(path.isAbsolute(file) && fs.statSync(file).isFile());
    assert.deepEqual(rhOk(clone, ['list']).trim().split('\t'), [r.id, 'open', file], 'open here, whatever it was there');
    const sheet = json(clone, ['sheet', '--all', file]);
    assert.equal(sheet.review, r.id);
    assert.equal(sheet.items.find(i => i.id === 'summary:1').fields.what, 'What it does.');

    const exportFile = path.join(tempDir(), 'shared.json');
    fs.writeFileSync(exportFile, exported);
    assert.equal(rhOk(clone, ['import', exportFile]).trim(), file, 'a second import merges into the same file');
});

test('import names each iteration whose head is missing here', { timeout: TIMEOUT }, () => {
    const r = review();
    const clone = cloneOf(r.dir);
    const head = commit(r.dir, 'Add more', { 'b.txt': 'b\n' });
    assert.equal(rhOk(r.dir, ['iterate', r.file]), '2\n');

    const res = rh(clone, ['import', '-'], rhOk(r.dir, ['export', r.file]));
    assert.equal(res.status, 0, res.stderr);
    const lines = res.stderr.split('\n').filter(l => /\biteration \d+\b/i.test(l));
    assert.equal(lines.length, 1, 'one iteration is named');
    assert.match(lines[0], /\biteration 2\b/i, 'iteration 2 is named');
    assert.ok(lines[0].includes(head), 'with its head');
});

test('import refuses what is not a review export it supports', { timeout: TIMEOUT }, () => {
    const r = review();
    const clone = cloneOf(r.dir);
    const data = JSON.parse(rhOk(r.dir, ['export', r.file]));
    for (const [what, bad] of [
        ['another format', { ...data, format: 'something-else' }],
        ['another version', { ...data, format_version: 999 }],
        ['a feedback table', { ...data, tables: { ...data.tables, feedback: { columns: ['id'], rows: [] } } }],
    ]) {
        const res = rh(clone, ['import', '-'], JSON.stringify(bad));
        assert.notEqual(res.status, 0, `${what} is refused`);
        assert.notEqual(res.stderr, '');
    }
    assert.equal(rhOk(clone, ['list']), '', 'nothing was registered');
});
