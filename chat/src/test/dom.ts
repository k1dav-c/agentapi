// A browser's document for the tests that render with effects
// (react-dom/client): happy-dom's, from before a file's first test until
// after its last, for the files that call withDOM at their top. The other
// test files render on the server and see none.
//
// The test files share their modules: Radix's parts that open (a dialog,
// a menu) pick their layout effect once, as the first file to load them
// has a document or not. They aren't for these tests; the browser checks
// them. The window is a desktop's, wide enough for the Issues page's panel
// (not its sheet, a dialog).

import {GlobalRegistrator} from "@happy-dom/global-registrator";
import {afterAll, beforeAll} from "bun:test";

export function withDOM() {
  beforeAll(() => {
    GlobalRegistrator.register({url: "http://hub.test/chat/", width: 1440, height: 900});
    // React's act() warns outside an environment that says it is a test's.
    (globalThis as {IS_REACT_ACT_ENVIRONMENT?: boolean}).IS_REACT_ACT_ENVIRONMENT = true;
  });
  afterAll(async () => {
    delete (globalThis as {IS_REACT_ACT_ENVIRONMENT?: boolean}).IS_REACT_ACT_ENVIRONMENT;
    await GlobalRegistrator.unregister();
  });
}
