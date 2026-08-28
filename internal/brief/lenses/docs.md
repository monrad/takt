Review documentation the diff makes stale or owes. First read the current README.md, the design specs
under docs/superpowers/specs/, and any agent contracts or --help text the diff touches — report a gap
only when it is not already documented.

1. Behaviour the diff changes that documentation still describes the old way.
2. New flags, commands, config keys or agent contracts with no documentation.
3. Comments in the changed code that now lie about what the code does.
4. Documented invariants the diff breaks without updating the document.

Skip: internal refactoring with no visible change; test-only changes; prose polish. A task whose own
job is documentation (class: docs) is judged by the intent lens against its description, not here.
