# Installing the Perl backend

The reference implementation of the standard. Shared steps (the
`review-helper` command, linking the skills) are in the root
[`INSTALL.md`](../INSTALL.md).

You need Perl 5.36 or later on a perl that can fork: Cygwin's perl works on
Windows, native Windows perl does not.

## From CPAN

    cpanm Review::Helper

installs `review-helper` and the backend, `review-helper-perl`.

## From a checkout

1. Install [Dist::Zilla](https://metacpan.org/dist/Dist-Zilla)
   (`cpanm Dist::Zilla`), then the Perl dependencies:

   ```
   cd perl
   dzil authordeps --missing | cpanm
   dzil listdeps --missing | cpanm
   ```

2. Either install it (`make perl-install`, which runs `dzil install`), or
   run it from the checkout: `perl/scripts/` holds both `review-helper` and
   `review-helper-perl`. Add this to your shell profile, with the checkout's
   absolute path:

   ```
   export PATH="/path/to/Review-Helper/perl/scripts:$PATH"
   export PERL5LIB="/path/to/Review-Helper/perl/lib:$PERL5LIB"
   ```

   `review-helper --which` should now name the Perl backend.

`make perl-test` runs its tests (`prove -lr t`); `perl/AGENTS.md` gives the
full test command.

The user manual has the details: `perldoc Review::Helper::Manual`, or
`perldoc perl/lib/Review/Helper/Manual.pm` from a checkout.
