// serve, feedback (--open, --publish), published, and requests in the sheet.
// Feedback is written the way the page writes it, through the HTTP API.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TIMEOUT, rhOk, json, makeRepo, commit, git, fill, filled, serve, post } from './lib.mjs';

const REVIEWER = { REVIEW_HELPER_REVIEWER: 'Tess Tester <tess@example.com>' };
const byId = list => [...list].sort((a, b) => a.id - b.id);

test('feedback, its open subset, the publish list and published', { timeout: TIMEOUT }, async () => {
    const dir = makeRepo();
    const base = git(dir, 'rev-parse', 'HEAD');
    const head = commit(dir, 'Add six lines', { 'six.txt': 'one\ntwo\nthree\nfour\nfive\nsix\n' });
    const file = rhOk(dir, ['build', '--base', base, '--source', 'https://example.com/pr/1']).trim();
    assert.equal(fill(dir, file, filled(json(dir, ['sheet', file]))).status, 0);

    const server = await serve(dir, ['serve', file], REVIEWER);
    let ids;
    try {
        assert.match(server.url, /^https?:\/\/\S+$/, 'serve prints the URL first');
        ids = {
            review: (await post(server.url, 'api/feedback', { target: 'review', body: 'Looks fine.' })).id,
            file: (await post(server.url, 'api/feedback', { target: 'file', commit: head, path: 'six.txt', body: 'Split this file?' })).id,
            line: (await post(server.url, 'api/feedback', { target: 'line', commit: head, path: 'six.txt', line: 2, side: 'new', body: 'Why?' })).id,
        };
        await post(server.url, 'api/request', { kind: 'debate', target: 'file', commit: head, path: 'six.txt', note: 'Is six enough?' });
    }
    finally { await server.stop() }

    const all = json(dir, ['feedback', file]);
    assert.equal(all.format, 'review-feedback');
    assert.equal(all.format_version, 1);
    assert.equal(all.source, 'https://example.com/pr/1');
    assert.equal(all.iteration, 1);
    assert.deepEqual([all.base, all.head], [base, head]);
    assert.deepEqual(byId(all.feedback).map(f => [f.id, f.target, f.body, f.status, f.reviewer]), [
        [ids.review, 'review', 'Looks fine.', 'open', 'Tess Tester <tess@example.com>'],
        [ids.file, 'file', 'Split this file?', 'open', 'Tess Tester <tess@example.com>'],
        [ids.line, 'line', 'Why?', 'open', 'Tess Tester <tess@example.com>'],
    ]);
    const line = all.feedback.find(f => f.id === ids.line);
    assert.deepEqual([line.commit, line.path, line.line, line.side, line.published], [head, 'six.txt', 2, 'new', null]);
    assert.deepEqual(all.requests.map(r => [r.kind, r.target, r.path, r.note, r.status]), [['debate', 'file', 'six.txt', 'Is six enough?', 'open']]);

    // The agent answers through the sheet.
    const sheet = json(dir, ['sheet', file]);
    const answer = sheet.items.find(i => i.id === `feedback:${ids.review}`);
    const debate = sheet.items.find(i => i.type === 'debate');
    assert.ok(answer && debate, 'open feedback and debates are in the sheet');
    const bare = fill(dir, file, { ...sheet, items: [{ ...answer, fields: { status: 'addressed', response: null } }] });
    assert.notEqual(bare.status, 0, 'a status needs a response');
    assert.match(bare.stderr, new RegExp(answer.id));
    const res = fill(dir, file, { ...sheet, items: [
        { ...answer, fields: { status: 'addressed', response: 'Thanks.' } },
        { ...debate, fields: { case_for: 'It is enough.', case_against: 'Seven is luckier.' } },
    ] });
    assert.equal(res.status, 0, res.stderr);
    const answered = json(dir, ['feedback', file]);
    assert.deepEqual(['status', 'response'].map(k => answered.feedback.find(f => f.id === ids.review)[k]), ['addressed', 'Thanks.']);
    assert.deepEqual(['case_for', 'case_against'].map(k => answered.requests[0][k]), ['It is enough.', 'Seven is luckier.']);
    assert.deepEqual(byId(json(dir, ['feedback', file, '--open']).feedback).map(f => f.id), [ids.file, ids.line], '--open leaves out the addressed one');

    const publish = json(dir, ['feedback', file, '--publish']);
    assert.equal(publish.format, 'review-publish');
    assert.equal(publish.format_version, 1);
    assert.equal(publish.head, head);
    assert.deepEqual(publish.items, [
        { id: ids.file, text: '`six.txt`\n\nSplit this file?', anchor: null, fallback: null },
        { id: ids.line, text: 'Why?', anchor: { path: 'six.txt', line: 2, side: 'new' },
          fallback: `\`six.txt:2\` (commit ${head.slice(0, 7)})\n\nWhy?` },
    ]);

    assert.equal(rhOk(dir, ['published', file, String(ids.file)]), '');
    assert.notEqual(json(dir, ['feedback', file]).feedback.find(f => f.id === ids.file).published, null);
    assert.deepEqual(json(dir, ['feedback', file, '--publish']).items.map(i => i.id), [ids.line], 'a posted item is not listed again');
    assert.deepEqual(json(dir, ['feedback', file, '--publish', String(ids.file), String(ids.review)]).items.map(i => i.id),
        [ids.file, ids.review], 'items asked for by id are listed, in that order');
});

test('a review file alone as the first argument serves it', { timeout: TIMEOUT }, async () => {
    const dir = makeRepo();
    const base = git(dir, 'rev-parse', 'HEAD');
    commit(dir, 'Add a file', { 'a.txt': 'a\n' });
    const file = rhOk(dir, ['build', '--base', base]).trim();
    const id = rhOk(dir, ['list']).split('\t')[0];
    const server = await serve(dir, [file]);
    try {
        const res = await fetch(new URL('api/review', server.url));
        assert.equal(res.status, 200);
        assert.equal((await res.json()).review.id, id);
    }
    finally { await server.stop() }
});
