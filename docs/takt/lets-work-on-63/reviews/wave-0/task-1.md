# Review: lets-work-on-63 task 1 — rework

The parser silently truncates a valid table when a data row without outer pipes begins with a hash character.

- **major** internal/spec/assumptions.go:97 — Valid pipe-separated rows beginning with `#` are mistaken for headings: Rows may omit outer pipes, as `cells` explicitly supports. A valid row such as `#123 | Yes | Required by the issue | user-confirmed` is therefore well formed, but this condition treats it as a heading and stops parsing, omitting that row and all following rows. Only actual Markdown headings should terminate row parsing (for example, a hash run followed by whitespace), while `#123` and similar cell content must be parsed normally.

_copilot / gpt-5.6-sol_
