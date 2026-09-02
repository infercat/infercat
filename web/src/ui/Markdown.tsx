// Assistant replies as markdown. Images are deliberately rendered as links: the page makes no
// external network requests except the DERP map the wasm fetches, and a model can emit any URL.
import { memo, useRef, useState, type ComponentProps } from 'react';
import ReactMarkdown, { type Components } from 'react-markdown';
import rehypeHighlight from 'rehype-highlight';
import remarkGfm from 'remark-gfm';

function CodeBlock({ node: _node, ...rest }: ComponentProps<'pre'> & { node?: unknown }) {
  const pre = useRef<HTMLPreElement>(null);
  const [copied, setCopied] = useState(false);
  return (
    <div className="codeblock">
      <button
        className="copy"
        onClick={() => {
          const text = pre.current?.textContent ?? '';
          void navigator.clipboard?.writeText(text);
          setCopied(true);
          setTimeout(() => setCopied(false), 1400);
        }}
      >
        {copied ? 'copied' : 'copy'}
      </button>
      <pre ref={pre} {...rest} />
    </div>
  );
}

const COMPONENTS: Components = {
  pre: CodeBlock,
  a: ({ node: _node, ...rest }) => <a {...rest} target="_blank" rel="noreferrer noopener" />,
  img: ({ node: _node, src, alt }) => (
    <a className="img-link" href={typeof src === 'string' ? src : undefined} target="_blank" rel="noreferrer noopener">
      {alt || 'image'} ↗
    </a>
  ),
  table: ({ node: _node, ...rest }) => (
    <div className="table-scroll">
      <table {...rest} />
    </div>
  ),
};

function MarkdownBody({ text }: { text: string }) {
  return (
    <div className="md">
      <ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeHighlight]} components={COMPONENTS}>
        {text}
      </ReactMarkdown>
    </div>
  );
}

export default memo(MarkdownBody);
