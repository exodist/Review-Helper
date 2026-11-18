// Build a fixture review for the page drive (page-drive.mjs): a throwaway
// git repository in DIR and a filled review of it, made with the
// review-helper command (bin/review-helper, so REVIEW_HELPER_IMPL picks the
// backend) and its HTTP API. Then it edits the review file directly: it
// adds a few rows the format allows but no command or route writes, and
// adjusts some the API wrote (clearing reviewers, setting a section,
// removing a seen mark) where the fixture needs what no route makes. Prints
// the review file's path; it does not serve it. With --topics, the review is
// a squashed one split into two topics. This file is the authoritative description of
// the fixtures' content; conformance/README.md, "Fixtures", says how to use it.
//
// Usage: node conformance/fixtures.mjs [--topics] DIR
import fs from 'node:fs';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import { git, commit, rhOk, json, fill, filled, serve, post } from './cli/lib.mjs';

const args = process.argv.slice(2);
const topics = args[0] === '--topics';
if (topics) args.shift();
if (args.length !== 1) {
    console.error('Usage: node conformance/fixtures.mjs [--topics] DIR');
    process.exit(2);
}
const dir = path.resolve(args[0]);
fs.mkdirSync(dir, { recursive: true });
if (fs.readdirSync(dir).length) {
    console.error(`${dir} is not empty`);
    process.exit(2);
}
git(dir, 'init', '-q', '-b', 'main');

// Feedback through the API always records a reviewer; posts that stand for
// feedback from a tool that records none go in under this name, which is
// cleared afterwards.
const NOBODY = 'fixture <nobody@example.com>';
const ANN = 'Ann Other <ann@example.com>';
const BOB = 'Bob Other';

const FOO_1 = `package Foo;
use v5.36;

sub new ($class, %args) {
    return bless {%args}, $class;
}
sub name ($self) {
    $self->{name};
}

1;

__END__

=head1 NAME

Foo - a thing

=head2 name

Returns the name.

=cut
`;

const FOO_2 = `package Foo;
use v5.36;

sub new ($class, %args) {
    return bless {%args}, $class;
}
sub name ($self) {
    $self->{name} // 'anon';
}
sub greet ($self) {
    return <<"EOT";
Hello, @{[ $self->name ]}! <b>&amp;</b>
EOT
}

1;

__END__

=head1 NAME

Foo - a thing

=head2 name

Returns the name, or C<anon>.

=head2 greet

Returns a greeting.

=cut
`;

const UTIL_2 = `/**
 * Join words with a separator.
 */
export function join(words, sep = ' ') {
    return words.join(sep);
}

export const shout = s => \`\${s.toUpperCase()}
!\`;
`;

// A module with several subs, each documented, long enough that its diff
// runs past the bottom of the window, with a constant between two subs.
const BAR_SUBS = ['start', 'stop', 'reset', 'count', 'total'];
const barSub = name => {
    let body = '';
    for (let i = 1; i <= 8; i++) body += `    my $step${i} = $self->{${name}} + ${i};\n`;
    return `sub ${name} ($self, @args) {\n${body}    return $step8;\n}\n\n`;
};
const BAR = 'package Bar;\nuse v5.36;\n\n'
    + BAR_SUBS.slice(0, 2).map(barSub).join('')
    + 'use constant LIMIT => 10;\n\n'
    + BAR_SUBS.slice(2).map(barSub).join('')
    + '1;\n\n__END__\n\n=head1 NAME\n\nBar - a counter\n\n'
    + BAR_SUBS.map(s => `=head2 ${s}\n\nThe ${s} step.\n\n`).join('')
    + '=cut\n';

const UTIL_3 = UTIL_2.split(/(?<=\n)/).slice(0, 6).join('');
const NOTES = Array.from({ length: 10 }, (_, i) => `Note ${i + 1}.\n`).join('');
const TODO = Array.from({ length: 4 }, (_, i) => `- Task ${i + 1}.\n`).join('');
const SVG_1 = '<svg xmlns="http://www.w3.org/2000/svg" width="40" height="40">\n<rect width="40" height="40" fill="red"/>\n</svg>\n';
const SVG_2 = '<svg xmlns="http://www.w3.org/2000/svg" width="40" height="40">\n<circle cx="20" cy="20" r="18" fill="blue"/>\n</svg>\n';
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==', 'base64');
const BYTES = Buffer.from(Array.from({ length: 16 }, (_, i) => i));

// Note a journal entry (with flag, a flag note); returns its id.
function note(fields) {
    const a = ['note'];
    for (const [k, v] of Object.entries(fields)) a.push(`--${k}`, v);
    return rhOk(dir, a).trim();
}

// Fill the items pick(item) gives fields for, from a fresh sheet.
function fillSheet(file, pick, all = false) {
    const r = fill(dir, file, filled(json(dir, all ? ['sheet', '--all', file] : ['sheet', file]), pick));
    if (r.status !== 0) throw new Error(`fill failed: ${r.stderr}`);
}

// Serve the review as reviewer while calls(url) runs.
async function asReviewer(file, reviewer, calls) {
    const { url, stop } = await serve(dir, ['serve', file], { REVIEW_HELPER_REVIEWER: reviewer });
    try { await calls(url) } finally { await stop() }
}

function lineAt(rev, file, n) {
    return git(dir, 'show', `${rev}:${file}`).split('\n')[n - 1];
}

const db = file => new DatabaseSync(file);

const output = topics ? await topicsReview() : await historyReview();
console.log(output);
process.exit(0);

async function historyReview() {
    const rev = {};
    rev.base = commit(dir, 'Start the notes', { 'COPYING': 'Free to use.\n', 'notes.txt': NOTES, 'share/icon.svg': SVG_1, 'TODO.txt': TODO, 'old.bin': BYTES });
    rev.c1 = commit(dir, 'Add Foo, a logo and some data', { 'lib/Foo.pm': FOO_1, 'share/logo.png': PNG, 'data.bin': BYTES });
    rev.c2 = commit(dir, 'Greet by name; add util.js', { 'lib/Foo.pm': FOO_2, 'js/util.js': UTIL_2, 'data.bin': null, 'share/icon.svg': SVG_2 });

    // Iteration 1 also has a commit that is gone by iteration 2.
    rev.old = commit(dir, 'Add old.js', { 'js/old.js': 'export const old = 1;\n' });
    const file = rhOk(dir, ['build', '--base', rev.base, '--title', 'Fixture review', '--source', 'https://example.com/org/repo/pull/7']).trim();
    git(dir, 'reset', '-q', '--hard', rev.c2);

    // The trailer is one long token, which must wrap at phone width.
    rev.c3 = commit(dir, 'Drop shout\n\nClaude-Session: https://example.com/code/session_0123456789abcdefghijklmnopqrstuvwxyz', { 'js/util.js': UTIL_3, 'TODO.txt': null, 'old.bin': null });

    // docs branches off, with a changelog branch merged into it, then merges back.
    git(dir, 'checkout', '-q', '-b', 'docs');
    rev.c4 = commit(dir, 'Add a README', { 'README.md': '# Foo\n\nFoo greets people by name.\n\nSee lib/Foo.pm.\n' });
    git(dir, 'checkout', '-q', '-b', 'changelog');
    rev.c5 = commit(dir, 'Start a changelog', { 'Changes.md': '# Changes\n\n- Added Foo.\n' });
    git(dir, 'checkout', '-q', 'docs');
    git(dir, 'merge', '-q', '--no-ff', '-m', "Merge branch 'changelog' into docs", 'changelog');
    git(dir, 'checkout', '-q', 'main');
    git(dir, 'merge', '-q', '--no-ff', '-m', "Merge branch 'docs'", 'docs');
    rev.c7 = git(dir, 'rev-parse', 'HEAD');
    rev.c6 = git(dir, 'rev-parse', 'HEAD^2');

    git(dir, 'mv', 'notes.txt', 'docs-notes.txt');
    git(dir, 'mv', 'COPYING', 'LICENSE');
    rev.c8 = commit(dir, 'Move the notes', { 'docs-notes.txt': NOTES + 'Note 11.\n' });
    rev.c9 = commit(dir, 'Add Bar, a counter', { 'lib/Bar.pm': BAR });
    const subject = Object.fromEntries(Object.entries(rev).map(([k, v]) => [v, k]));

    // Notes, and where the sheet places them: [commit, path, lines].
    const notes = [
        [{ flag: 'red', kind: 'deviation', path: 'lib/Foo.pm', unit: 'greet', sentence: 'greet returns a heredoc with markup, which the spec did not ask for.', detail: 'The plan asked for a plain greeting. Callers render the greeting as HTML, so the markup is intentional. If plain text is wanted, drop the tags and the heredoc.' }, ['c2', 'lib/Foo.pm', [10, 14]]],
        [{ flag: 'yellow', kind: 'note', path: 'js/util.js', sentence: 'shout spans two lines because of the template literal.', detail: 'The newline inside the literal is part of the output.' }, ['c2', 'js/util.js', [8, 9]]],
        [{ flag: 'yellow', kind: 'assumption', path: 'lib/Foo.pm', sentence: 'Assumed nothing else reads the name field directly.' }, ['c2', 'lib/Foo.pm', null]],
        [{ flag: 'red', kind: 'self-review', path: 'data.bin', sentence: 'The binary file was added by mistake and is removed in the next commit.', detail: 'Self-review caught it after committing.' }, ['c1', null, null]],
        [{ flag: 'yellow', kind: 'test-revealed', path: 'lib/Foo.pm', sentence: 'A test showed the constructor must copy the arguments.', detail: 'Blessing the hash of the caller let later changes leak back.' }, ['c1', 'lib/Foo.pm', [5, 5]]],
        [{ flag: 'red', kind: 'deviation', path: 'README.md', sentence: 'The README was not in the plan.' }, ['c4', 'README.md', null]],
        [{ flag: 'yellow', kind: 'note', path: 'Changes.md', sentence: 'The changelog starts with one entry.' }, ['c5', 'Changes.md', [3, 3]]],
        [{ flag: 'yellow', kind: 'note', path: 'docs-notes.txt', sentence: 'The notes keep their history through the move.' }, ['c8', 'docs-notes.txt', [11, 11]]],
        [{ flag: 'yellow', kind: 'note', path: 'TODO.txt', sentence: 'The TODO list goes; its tasks are done.' }, ['c3', 'TODO.txt', [2, 3]]],

        // Line 5 is in sub new, which the commit did not change, so in none of
        // the sections the page lists.
        [{ flag: 'yellow', path: 'lib/Foo.pm', sentence: 'The constructor is unchanged; callers still pass a name.' }, ['c2', 'lib/Foo.pm', [5, 5]]],
    ];
    const place = Object.fromEntries(notes.map(([n, p]) => [n.sentence, p]));
    const noteIds = notes.map(([n]) => note(n));
    const greetNote = noteIds[0];

    // Journal entries, which iteration 2 takes as they are: one on a file the
    // iteration changed, one on a file it did not.
    note({ kind: 'test-revealed', path: 'lib/Foo.pm', unit: 'greet', sentence: 'The greeting tests needed a fresh object each.', detail: "A shared object let one test's name leak into the next.\n\nEach test now builds its own." });
    note({ path: 'docs/plan.txt', sentence: 'The plan file was read, not changed.' });

    // Iteration 1 feedback, made before the rework.
    await asReviewer(file, NOBODY, async url => {
        await post(url, 'api/feedback', { iteration: 1, target: 'line', commit: rev.c2, path: 'lib/Foo.pm', line: 8, side: 'new', body: 'Why default to anon?' });
        await post(url, 'api/feedback', { iteration: 1, target: 'file', commit: rev.old, path: 'js/old.js', body: 'This file looks unused.' });
    });

    if (rhOk(dir, ['iterate', file]).trim() !== '2') throw new Error('iterate did not make iteration 2');

    await asReviewer(file, ANN, url => post(url, 'api/feedback', { iteration: 2, target: 'review', body: 'Please say why the logo is needed at all.' }));
    await asReviewer(file, NOBODY, async url => {
        await post(url, 'api/feedback', { iteration: 2, target: 'commit', commit: rev.c1, body: 'Do not commit binaries.' });
    });

    // The agent's split of each file, covering every line: subs with their POD,
    // headings, and the lines around them. share/icon.svg is left unsplit, to
    // show a file with no sections yet; docs-notes.txt is left whole.
    const barSplit = [[0, [4, 15], [74, 77]], [1, [16, 26], [78, 81]], [2, [30, 41], [82, 85]], [3, [42, 53], [86, 89]], [4, [54, 64], [90, 94]]]
        .map(([i, l, d]) => ({ name: `sub ${BAR_SUBS[i]}`, package: 'Bar', lines: [l], doc_lines: [d] }));
    const splits = {
        'c1 lib/Foo.pm': [
            { name: 'header', package: 'Foo', lines: [[1, 2]] },
            { name: 'sub new', package: 'Foo', lines: [[4, 6]] },
            { name: 'sub name', package: 'Foo', lines: [[7, 9]], doc_lines: [[19, 23]] },
            { name: 'end', package: 'Foo', lines: [[11, 13]] },
            { name: '=head1 NAME', package: 'Foo', lines: [[15, 18]] },
        ],
        'c2 lib/Foo.pm': [
            { name: 'header', package: 'Foo', lines: [[1, 2]] },
            { name: 'sub new', package: 'Foo', lines: [[4, 6]] },
            { name: 'sub name', package: 'Foo', lines: [[7, 9]], doc_lines: [[24, 27]], old_lines: [[7, 9], [19, 23]] },
            { name: 'sub greet', package: 'Foo', lines: [[10, 14]], doc_lines: [[28, 32]] },
            { name: 'end', package: 'Foo', lines: [[16, 18]] },
            { name: '=head1 NAME', package: 'Foo', lines: [[20, 23]] },
        ],
        'c2 js/util.js': [{ name: 'export function join', lines: [[4, 6]], doc_lines: [[1, 3]] }, { name: 'export const shout', lines: [[8, 9]] }],
        'c3 js/util.js': [{ name: 'export function join', lines: [[4, 6]], doc_lines: [[1, 3]], old_lines: [[1, 3], [4, 6]] }],
        'c4 README.md': [{ name: 'Foo', lines: [[1, 5]] }],
        'c5 Changes.md': [{ name: 'Changes', lines: [[1, 3]] }],
        'c8 docs-notes.txt': [],
        'c9 lib/Bar.pm': [
            ...barSplit,
            { name: 'header', package: 'Bar', lines: [[1, 2]] },
            { name: 'use constant LIMIT', package: 'Bar', lines: [[28, 28]] },
            { name: 'end', package: 'Bar', lines: [[66, 68]] },
            { name: '=head1 NAME', package: 'Bar', lines: [[70, 73]] },
        ],
    };
    // First lines are taken from the file, as an agent would copy them.
    const withFirstLines = (about, sections) => sections.map(s => {
        const c = { ...s };
        if (s.lines?.length) c.first_line = lineAt(about.commit, about.path, Math.min(...s.lines.map(r => r[0])));
        if (s.old_lines?.length) c.old_first_line = lineAt(`${about.commit}^`, about.old_path ?? about.path, Math.min(...s.old_lines.map(r => r[0])));
        return c;
    });
    fillSheet(file, item => {
        if (item.type !== 'split') return null;
        const s = splits[`${subject[item.about.commit]} ${item.about.path}`];
        return s ? { sections: withFirstLines(item.about, s) } : null;
    });

    // Section anchors need the split, so the rest of iteration 2's feedback
    // and requests come after it, in the order a reviewer made them.
    await asReviewer(file, NOBODY, async url => {
        await post(url, 'api/feedback', { iteration: 2, target: 'section', commit: rev.c2, path: 'lib/Foo.pm', section: { name: 'sub greet', package: 'Foo' }, body: 'Could greet take a greeting word?' });
        await post(url, 'api/feedback', { iteration: 2, target: 'line', commit: rev.c2, path: 'lib/Foo.pm', line: 8, side: 'old', body: 'The old accessor had no default; was anything relying on undef?' });
        await post(url, 'api/request', { iteration: 2, kind: 'debate', target: 'file', commit: rev.c2, path: 'lib/Foo.pm', note: 'Is greet worth adding?' });
        await post(url, 'api/request', { iteration: 2, kind: 'split', target: 'file', commit: rev.c2, path: 'js/util.js', note: 'Split shout from join.' });
        await post(url, 'api/request', { iteration: 2, kind: 'debate', target: 'section', commit: rev.c2, path: 'lib/Foo.pm', section: { name: 'sub greet', package: 'Foo' } });
        await post(url, 'api/request', { iteration: 2, kind: 'debate', target: 'commit', commit: rev.c3, note: 'Was dropping shout right?' });
    });

    // Feedback a re-split moved to its file, keeping the section it was on;
    // no route anchors file feedback to a section.
    {
        const d = db(file);
        const now = Math.floor(Date.now() / 1000);
        d.prepare("INSERT INTO feedback (iteration, target, revision, path, section_name, body, created, updated, status) VALUES (2, 'file', ?, 'js/util.js', 'const shout', 'Is the newline in shout intended?', ?, ?, 'open')").run(rev.c2, now, now);
        d.close();
    }

    const commitText = {
        c1: { what: 'Adds the `Foo` class and two assets.', why: 'Foo is the base of the change.', how: 'A plain blessed hash.' },
        c3: { what: 'Removes `shout`.', why: 'Nothing used it.', how: 'Deletes the export.' },
        c4: { what: 'Documents `Foo` for new readers.\n\nOne short page.', why: 'New readers asked.', how: 'A README.' },
        c6: { what: '- Brings in the changelog.', why: 'The docs branch needs it.', how: 'A merge.' },
        c7: { what: 'Merges the docs branch: a README and a changelog.', why: 'Docs are done.', how: 'A merge.' },
    };
    const fileText = {
        'c1 lib/Foo.pm': { what: 'Adds the `Foo` class with a constructor and a `name` accessor.', object_system: 'plain bless', paradigms: 'OOP' },
        'c1 share/logo.png': { what: 'A placeholder logo.' },
        'c2 lib/Foo.pm': { what: 'Defaults the name to `anon` and adds `greet`.', object_system: 'plain bless', paradigms: 'OOP' },
        'c3 js/util.js': { what: 'Drops the unused `shout` helper from `Util::Shout::Helpers::For::Very::Loud::Greetings::And::More`.' },
        'c4 README.md': { what: 'Introduces `Foo`.' },
        'c9 lib/Bar.pm': { what: 'Adds the `Bar` counter with five steps.', object_system: 'plain bless', paradigms: 'OOP' },
    };
    const sectionText = {
        'c2 lib/Foo.pm sub name': 'Falls back to `anon` when no name was given.\n\n- POD updated to match.',
        'c9 lib/Bar.pm sub start': 'Starts the count from the stored value.',
        'c9 lib/Bar.pm sub stop': 'Stops the count and returns where it got to.',
    };
    const order = {
        c1: ['share/logo.png', 'lib/Foo.pm', 'data.bin'],
        c3: ['js/util.js', 'TODO.txt', 'old.bin'],
        c4: ['README.md'],
        c5: ['Changes.md'],
        c8: ['docs-notes.txt', 'LICENSE'],
        c9: ['lib/Bar.pm'],
    };
    const mark = (p, side, s, e) => ({ path: p, side, lines: [s, e] });
    const guide = [
        [{ type: 'summary' }, [], 'This change adds a small class and a helper script, then trims the helper.'],
        [{ type: 'commit', commit: rev.c1 }, [], 'The first commit lays the groundwork: the class, a logo, and a data file.'],
        [{ type: 'file', commit: rev.c2, path: 'lib/Foo.pm' }, [], 'In the second commit the Foo module gains a default name and a greeting.'],
        [{ type: 'section', commit: rev.c2, path: 'lib/Foo.pm', section: { name: 'sub greet', package: 'Foo' } }, [mark('lib/Foo.pm', 'new', 11, 13)], 'The greeting builds its text with a heredoc.'],
        [{ type: 'diff', commit: rev.c2, path: 'lib/Foo.pm', section: { name: 'sub name', package: 'Foo' } }, [mark('lib/Foo.pm', 'new', 8, 8)], 'The highlighted line now falls back to anon when no name was given.'],
        [{ type: 'flag', flag: greetNote }, [], 'The agent flagged the greeting because the markup was not in the plan.'],
        [{ type: 'view', commit: rev.c2, path: 'js/util.js', lines: [1, 6] }, [mark('js/util.js', 'new', 4, 6)], 'The helper script joins words; the highlighted function does the work.'],
        [{ type: 'diff', commit: rev.c3 }, [mark('js/util.js', 'old', 8, 9)], 'The last commit drops the shout helper, shown highlighted on the old side.'],
        [{ type: 'commit', commit: rev.c5 }, [], 'The changelog came in through a nested merge on the docs branch.'],
        [{ type: 'view', commit: rev.c8, path: 'lib/Foo.pm', lines: [10, 14] }, [mark('lib/Foo.pm', 'new', 11, 13)], 'At the head the greeting reads the same; the highlighted heredoc holds the markup.'],

        // The highlight starts above the diff's first hunk, so its first line is
        // not on the page.
        [{ type: 'diff', commit: rev.c2, path: 'lib/Foo.pm' }, [mark('lib/Foo.pm', 'new', 4, 8)], 'Back in the second commit, the constructor is unchanged above the new default.'],
    ];
    const feedbackReply = {
        'Why default to anon?': { status: 'addressed', response: 'The default is now documented in the POD.' },
        'Do not commit binaries.': { status: 'declined', response: 'The binary is removed in the next commit.' },
        'This file looks unused.': { status: 'addressed', response: 'Removed.' },
    };
    const debateReply = {
        'Is greet worth adding?': {
            case_for: 'Callers built the same greeting in three places.\n\n- one place to change the markup\n- tested once',
            case_against: 'A heredoc with markup mixes presentation into the model.\n\n- a template would keep it out\n- the three callers could share a helper instead',
        },
    };

    fillSheet(file, item => {
        const a = item.about;
        switch (item.type) {
            case 'summary': return {
                what: 'A tiny change set used to exercise the page.\n\nIt covers:\n- added, modified, deleted and renamed files\n- an image and a binary\n- a commit with no reading order yet',
                why: 'The page needs data that touches every rendering path.',
                how: 'Small commits, two merges, and a second iteration.',
                note: 'Drops `shout` after feedback.\n\n- Keeps `greet`.',
            };
            case 'commit': return commitText[subject[a.revision]] ?? null;
            case 'order': { const o = order[subject[a.commit]]; return o ? { paths: o } : null }
            case 'file': return fileText[`${subject[a.commit]} ${a.path}`] ?? null;
            case 'section': { const t = sectionText[`${subject[a.commit]} ${a.path} ${a.name}`]; return t ? { what: t } : null }
            case 'note': { const [c, p, lines] = place[a.sentence]; return { commit: rev[c], path: p, lines } }
            case 'feedback': return feedbackReply[a.body] ?? null;
            case 'debate': return a.note != null ? debateReply[a.note] ?? null : null;
            case 'guide': return { steps: guide.map(([target, highlight, commentary]) => ({ target, highlight, commentary })) };
            default: return null;
        }
    });

    // Placed notes are flags now; the agent turns the binary file's yellow, as
    // the next commit removes the file.
    fillSheet(file, item => item.type === 'flag' && item.about.sentence.startsWith('The binary file')
        ? { decision: 'recolor', color: 'yellow', reason: 'Removed in the next commit, as planned.' } : null, true);

    const d = db(file);
    const flagId = start => d.prepare('SELECT flag.id FROM flag JOIN commit_entry ON commit_entry.id = flag.commit_id WHERE iteration = 2 AND sentence LIKE ?').get(`${start}%`).id;
    const entryId = r => d.prepare('SELECT id FROM commit_entry WHERE iteration = 2 AND revision = ?').get(r).id;

    // Another reviewer has seen the README flag; Ann comments on the greet flag.
    await asReviewer(file, BOB, url => post(url, `api/flag/${flagId('The README')}`, { seen: true }));
    await asReviewer(file, ANN, url => post(url, 'api/feedback', { iteration: 2, target: 'flag', flag: greetNote, body: 'Is the markup tested?' }));

    const now = Math.floor(Date.now() / 1000);
    // Commenting on a flag marks it seen by the commenter; here only Bob's
    // mark stands, as from a tool that recorded the comment alone.
    d.prepare('DELETE FROM flag_seen WHERE reviewer = ?').run(ANN);

    // The iteration 1 line comment names its section, as a page showing a
    // split would have sent it; iteration 1 had no split for the API to check.
    d.prepare("UPDATE feedback SET section_name = 'sub name', section_package = 'Foo' WHERE body = 'Why default to anon?'").run();

    // Feedback from a tool that records no reviewer.
    d.prepare('UPDATE feedback SET reviewer = NULL WHERE reviewer = ?').run(NOBODY);
    d.prepare('UPDATE request SET reviewer = NULL WHERE reviewer = ?').run(NOBODY);

    // A flag on a file its commit did not change, as a squashed iteration
    // carries one when the new squash no longer touches the file; the sheet
    // places none.
    d.prepare("INSERT INTO flag (note_id, commit_id, path, line_start, line_end, color, kind, sentence, created, pending) VALUES ('carried-foo', ?, 'lib/Foo.pm', 3, 4, 'yellow', 'note', 'Bar copies the constructor of Foo.', ?, 1)").run(entryId(rev.c9), now);

    // A line flag on a binary, whose diff panel has no line table. The sheet
    // rejects lines on a binary and carrying a flag widens them, but the format
    // does not forbid the row, so a review file written by another tool can hold
    // one; it stands in for any line flag whose panel shows no lines.
    d.prepare("INSERT INTO flag (note_id, commit_id, path, line_start, line_end, color, kind, sentence, created) VALUES ('gone-bin', ?, 'old.bin', 1, 1, 'yellow', 'note', 'The old blob goes with nothing reading it.', ?)").run(entryId(rev.c3), now);

    // A line flag on an image: its file tab opens with a picture and no line
    // table, and the flag falls back to the file head once the panel settles.
    d.prepare("INSERT INTO flag (note_id, commit_id, path, line_start, line_end, color, kind, sentence, created) VALUES ('logo-line', ?, 'share/logo.png', 1, 1, 'yellow', 'note', 'The logo picture is a placeholder.', ?)").run(entryId(rev.c1), now);

    // Section feedback on a section its file does not show (an earlier split's):
    // the summary lists it.
    d.prepare("INSERT INTO feedback (iteration, target, revision, path, section_name, section_package, body, created, updated, status) VALUES (1, 'section', ?, 'lib/Foo.pm', 'sub gone', 'Foo', 'Was sub gone needed?', ?, ?, 'open')").run(rev.c2, now, now);

    // A last guide step on a section the page does not show, as a re-split after
    // the guide was written leaves one: it shows its commentary only.
    d.prepare("INSERT INTO guide_step (iteration, position, target, highlight, commentary) VALUES (2, 12, ?, '[]', 'This section was split away after the guide was written.')")
        .run(JSON.stringify({ commit: rev.c2, path: 'lib/Foo.pm', section: { name: 'sub gone', package: 'Foo' }, type: 'section' }));
    d.close();
    return file;
}

// A squashed review of a small codebase, split into two topics: the Perl
// backend and the frontend, with a flag and topic feedback in the second,
// feedback on the whole change from before the split, and a guide that plays
// the frontend topic, then its flag.
async function topicsReview() {
    const head = commit(dir, 'Land the app', { 'lib/Foo.pm': FOO_2, 'lib/Bar.pm': BAR, 'js/util.js': UTIL_2, 'share/icon.svg': SVG_1 });
    note({ flag: 'yellow', kind: 'note', path: 'js/util.js', sentence: 'join takes a separator.' });
    const file = rhOk(dir, ['build', '--squash', '--title', 'Topics review']).trim();
    await asReviewer(file, NOBODY, url => post(url, 'api/feedback', { iteration: 1, target: 'commit', commit: head, body: 'Review the whole app.' }));

    fillSheet(file, item => item.type === 'topics'
        ? { topics: [{ name: 'Perl backend', paths: ['lib/Bar.pm', 'lib/Foo.pm'] }, { name: 'Frontend', paths: ['js/util.js', 'share/icon.svg'] }] } : null);
    const what = { 'Perl backend': 'The `Foo` and `Bar` modules.', Frontend: 'The page script and its icon.' };
    fillSheet(file, item => {
        if (item.type === 'summary') return { what: 'A small app.', why: 'To review it as it lands.', how: 'Two topics.' };
        if (item.type === 'commit') return { what: what[item.about.topic], why: 'They land together.', how: 'Read them in order.' };
        if (item.type === 'note') return { commit: head, path: 'js/util.js', lines: [4, 6] };
        return null;
    });
    const d = db(file);
    const { note_id: flag } = d.prepare('SELECT note_id FROM flag').get();
    fillSheet(file, item => item.type === 'guide' ? {
        steps: [
            { target: { type: 'commit', commit: head, topic: 'Frontend' }, highlight: [], commentary: 'The frontend.' },
            { target: { type: 'flag', flag }, highlight: [], commentary: 'Its flag.' },
        ],
    } : null);
    await asReviewer(file, NOBODY, url => post(url, 'api/feedback', { iteration: 1, target: 'commit', commit: head, topic: 'Frontend', body: 'Is the icon final?' }));
    d.prepare('UPDATE feedback SET reviewer = NULL WHERE reviewer = ?').run(NOBODY);
    d.close();
    return file;
}
