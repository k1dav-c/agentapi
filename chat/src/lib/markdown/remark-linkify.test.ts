import {describe, expect, test} from "bun:test";
import {remarkLinkify, type MarkdownNode} from "./remark-linkify";

const text = (value: string): MarkdownNode => ({type: "text", value});
const link = (url: string, label = url): MarkdownNode => ({type: "link", url, children: [text(label)]});
const paragraph = (...children: MarkdownNode[]): MarkdownNode => ({type: "root", children: [{type: "paragraph", children}]});
const linkified = (tree: MarkdownNode) => {
  remarkLinkify()(tree);
  return tree.children![0].children;
};

describe("remarkLinkify", () => {
  test("links a bare URL in text, and leaves the text around it", () => {
    expect(linkified(paragraph(text("see https://example.com/a and more")))).toEqual([text("see "), link("https://example.com/a"), text(" and more")]);
  });

  test("a URL stops at Chinese punctuation", () => {
    expect(linkified(paragraph(text("見 https://example.com/path，下一段")))).toEqual([text("見 "), link("https://example.com/path"), text("，下一段")]);
  });

  test("a GFM autolink that ran on into Chinese punctuation is cut there, with its URL", () => {
    expect(linkified(paragraph(link("http://www.example.com/path，下一段", "www.example.com/path，下一段")))).toEqual([
      link("http://www.example.com/path", "www.example.com/path"),
      text("，下一段"),
    ]);
  });

  test("a [label](url) link keeps its label and URL", () => {
    const labelled = link("https://example.com/a", "文件：說明");
    expect(linkified(paragraph(labelled))).toEqual([labelled]);
  });

  test("code stays as it is, and text in emphasis is linked", () => {
    const code: MarkdownNode = {type: "inlineCode", value: "https://example.com"};
    expect(linkified(paragraph(code))).toEqual([code]);
    const emphasis: MarkdownNode = {type: "emphasis", children: [text("https://example.com")]};
    expect(linkified(paragraph(emphasis))).toEqual([{type: "emphasis", children: [link("https://example.com")]}]);
  });
});
