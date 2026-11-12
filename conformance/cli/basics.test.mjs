// version, --which, note, skills path, and errors.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { TIMEOUT, rh, rhOk, makeRepo, journal, tempDir, git } from './lib.mjs';

test('version prints the standard, then the implementation', { timeout: TIMEOUT }, () => {
    const out = rhOk(tempDir(), ['version']);
    assert.match(out, /^standard 1\nimplementation [^ \n]+ [^ \n]+\n$/);
});

test('--which lists the implementations, marks the one it runs, then prints its versions', { timeout: TIMEOUT }, () => {
    const dir = tempDir();
    const lines = rhOk(dir, ['--which']).split('\n');
    const listed = [];
    while (/^[* ] /.test(lines[0])) listed.push(lines.shift());
    const found = listed.map(line => {
        const m = /^([* ]) ([A-Za-z0-9_-]+) (.+)$/.exec(line);
        assert.ok(m, `a listed implementation: ${line}`);
        const [, mark, impl, file] = m;
        assert.equal(path.basename(file), `review-helper-${impl}`);
        assert.ok(fs.statSync(path.resolve(dir, file)).isFile());
        return { mark, impl };
    });
    assert.equal(new Set(found.map(f => f.impl)).size, found.length, 'each implementation once');
    const chosen = found.filter(f => f.mark === '*');
    assert.equal(chosen.length, 1, 'one is marked');
    if (process.env.REVIEW_HELPER_IMPL) assert.equal(chosen[0].impl, process.env.REVIEW_HELPER_IMPL);
    else assert.equal(found[0], chosen[0], 'the first found runs');
    assert.equal(lines.join('\n'), rhOk(dir, ['version']));
});

test('note records a journal entry by default, a flag with --flag', { timeout: TIMEOUT }, () => {
    const dir = makeRepo();
    const entry = rhOk(dir, ['note', '--path', 'README', '--sentence', 'Worth knowing.']);
    assert.match(entry, /^\S+\n$/);
    const flag = rhOk(dir, ['note', '--flag', 'red', '--kind', 'deviation', '--path', 'README', '--unit', 'intro',
        '--snippet', 'Scratch.', '--sentence', 'Probably wrong.', '--detail', 'The whole story.']);

    const notes = journal(dir);
    assert.equal(notes.length, 2);
    assert.deepEqual(notes.map(n => n.id), [entry.trim(), flag.trim()]);
    const [e, f] = notes;
    assert.equal(e.flag ?? null, null, 'no flag: a journal entry');
    assert.equal(e.kind, 'other', 'the kind defaults to other');
    assert.equal(e.path, 'README');
    assert.equal(e.sentence, 'Worth knowing.');
    assert.ok(Number.isInteger(e.created));
    assert.deepEqual([f.flag, f.kind, f.unit, f.snippet, f.sentence, f.detail],
        ['red', 'deviation', 'intro', 'Scratch.', 'Probably wrong.', 'The whole story.']);
    assert.equal(rhOk(dir, ['note', '--flag', 'yellow', '--path', 'README', '--sentence', 'May object.']).length > 1, true);
    assert.equal(journal(dir).at(-1).flag, 'yellow');
});

test('note refuses a bad color, a bad kind, and a missing path or sentence', { timeout: TIMEOUT }, () => {
    const dir = makeRepo();
    for (const args of [
        ['--flag', 'green', '--path', 'README', '--sentence', 'x'],
        ['--kind', 'gossip', '--path', 'README', '--sentence', 'x'],
        ['--sentence', 'x'],
        ['--path', 'README'],
    ]) {
        const r = rh(dir, ['note', ...args]);
        assert.notEqual(r.status, 0, `note ${args.join(' ')} fails`);
        assert.equal(r.stdout, '');
        assert.notEqual(r.stderr, '', 'and says why');
    }
    assert.deepEqual(journal(dir), [], 'nothing was recorded');
});

test('skills path prints the directory of the shipped skills', { timeout: TIMEOUT }, () => {
    const out = rhOk(tempDir(), ['skills', 'path']);
    assert.match(out, /^[^\n]+\n$/);
    const dir = out.trim();
    // Every backend ships the repository's skills/.
    const skills = fs.readdirSync(new URL('../../skills/', import.meta.url)).filter(s => !s.startsWith('.'));
    assert.ok(skills.length > 0);
    for (const skill of skills) assert.ok(fs.existsSync(path.join(dir, skill, 'SKILL.md')), `${skill} is there`);
});

test('errors go to standard error with a non-zero exit', { timeout: TIMEOUT }, () => {
    const dir = makeRepo();
    for (const args of [['sheet', path.join(dir, 'missing.sqlite')], ['build'], ['fill', 'only-one']]) {
        const r = rh(dir, args);
        assert.notEqual(r.status, 0, `${args.join(' ')} fails`);
        assert.equal(r.stdout, '', 'nothing on standard output');
        assert.notEqual(r.stderr, '');
    }
    const outside = tempDir();
    assert.throws(() => git(outside, 'rev-parse', '--git-dir'), 'the scratch directory is in no repository');
    const r = rh(outside, ['list']);
    assert.notEqual(r.status, 0, 'outside a repository');
    assert.notEqual(r.stderr, '');
});
