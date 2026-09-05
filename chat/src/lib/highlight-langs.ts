/**
 * Selective highlight.js language imports for rehype-highlight.
 * Only common coding-agent languages are included to keep bundle ~50KB
 * instead of the full ~900KB highlight.js distribution.
 */
import { common } from "lowlight";

// lowlight's `common` export includes the ~35 most-used languages:
// bash, c, cpp, csharp, css, diff, go, graphql, ini, java, javascript,
// json, kotlin, less, lua, makefile, markdown, objectivec, perl, php,
// php-template, plaintext, python, python-repl, r, ruby, rust, scss,
// shell, sql, swift, typescript, vbnet, wasm, xml, yaml
//
// This covers the vast majority of what coding agents produce.
// To add a niche language, import it from highlight.js and register:
//   import haskell from "highlight.js/lib/languages/haskell";
//   common.register("haskell", haskell);

export { common };
