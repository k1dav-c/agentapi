import {expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import {hostPrefix} from "@/lib/hub-machines";
import {HostName} from "./assign-menu";

// A pick dims the start the machines' names share, which is cut short
// first, so the rest tells them apart; the whole name is in its title.
test("a machine's name keeps what tells it apart", () => {
  const names = ["coder-k1dave6412-coder-template", "coder-k1dave6412-dave-sm", "coder-k1dave6412-yeh-deployment"];
  const prefix = hostPrefix(names);
  expect(prefix).toBe("coder-k1dave6412-");
  const html = renderToStaticMarkup(<HostName hostname={names[1]} prefix={prefix} />);
  expect(html).toContain('title="coder-k1dave6412-dave-sm"');
  expect(html).toMatch(/shrink-\[1000\][^>]*>coder-k1dave6412-<\/span><span class="min-w-0 truncate">dave-sm<\/span>/);
  // Without a shared start, the name is whole.
  expect(renderToStaticMarkup(<HostName hostname="pve-dev" prefix="" />)).toContain('<span class="min-w-0 truncate">pve-dev</span>');
});
