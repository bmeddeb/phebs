# Markdown preview receipt

This frozen document exercises **formatted prose**, *emphasis*, `inline code`,
and a [safe external link](https://example.invalid/markdown-preview).

> Repository Markdown is untrusted and sanitized before it is displayed.

## Review checklist

- Source remains the default view.
- Preview state is carried in the URL.
- Repository images remain named placeholders: ![Service boundary](./service-boundary.png)

| Surface | Expected result |
| --- | --- |
| Prose | Readable hierarchy and spacing |
| Links | Safe new browsing context |
| Images | No repository fetch |

```go
package receipt

const state = "deterministic"
```
