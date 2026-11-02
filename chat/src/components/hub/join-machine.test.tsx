import {describe, expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import {SECRET_MASK} from "@/lib/hub-join";
import {JoinSteps} from "./join-machine";

const SECRET = "9f3a51c0de7b44e2";
const lan = {url: "http://192.168.9.210:3290/hooks", interface: "eth0"};
const vpn = {url: "http://100.116.90.98:3290/hooks", interface: "tailscale0"};

// The text of html, without its tags.
const text = (html: string) => html.replace(/<[^>]+>/g, " ").replaceAll("&#x27;", "'").replaceAll("&lt;", "<").replaceAll("&gt;", ">").replace(/\s+/g, " ");

describe("JoinSteps", () => {
  test("the hub's URLs to pick from, the first picked, and the secret masked", () => {
    const html = renderToStaticMarkup(<JoinSteps root="https://hub.example.com" info={{urls: [lan, vpn], secret: SECRET, secret_shown: true}} />);
    const shown = text(html);
    expect(html).not.toContain(SECRET);
    expect(shown).toContain("http://192.168.9.210:3290/hooks · eth0");
    expect(shown).toContain("http://100.116.90.98:3290/hooks · tailscale0");
    expect(shown).toContain(`AGENTAPI_HUB_SECRET='${SECRET_MASK}' agentapi hub join http://192.168.9.210:3290/hooks --public-url 'http://{ip}:{port}'`);
    expect(shown).toContain(`The hub secret: \`${SECRET_MASK}\``);
    expect(shown).toContain("Show the secret");
    expect(shown).toContain("{ip} is the machine's address on the route to the hub");
    expect(shown).toContain("so the hub follows the machine when it gets another address");
    expect(shown).toContain("It needs AgentAPI 2.1.3 or later there");
    expect(shown).not.toContain("{port}' --allow-update");
    expect(shown).not.toContain("never shows its secret");
  });

  test("a hub without a token: a placeholder, and why", () => {
    const shown = text(renderToStaticMarkup(<JoinSteps root="" info={{urls: [lan], secret_shown: false}} />));
    expect(shown).toContain("AGENTAPI_HUB_SECRET='<hub secret>' agentapi hub join");
    expect(shown).toContain("A hub without a token never shows its secret: put in the one given to agentapi hub serve.");
    expect(shown).not.toContain("Show the secret");
  });

  test("no address found: the page's own, and what it takes", () => {
    const shown = text(renderToStaticMarkup(<JoinSteps root="http://hub.vpn:3290" info={{urls: [], secret: SECRET, secret_shown: true}} />));
    expect(shown).toContain("agentapi hub join http://hub.vpn:3290/hooks");
    expect(shown).toContain("The hub found no address of its machine, so this is the page's address: it works only without a sign-in page in front");
  });
});
