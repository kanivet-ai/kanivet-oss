import React, { useState } from 'react';
import { ChevronDownIcon, ChevronRightIcon, Cross2Icon } from '@radix-ui/react-icons';
import ResourceLink from '../ResourceLink';
import type { FindingResource, Findings, InvestigationEvent } from '../../services/api/ai';
import type { ContextChip } from '../../services/api/aiChat';
import { apiVersionFor, parseInline, parseMarkdown, refFromCode } from './aiUtils';
import { SparkleIcon, ToolIcon } from './AIIcons';
import { stripPatchBlocks, toolVerb } from './chatUtils';

export const Ref: React.FC<{ r: FindingResource; cluster: string; chip?: boolean }> = ({ r, cluster, chip }) => (
  <ResourceLink
    cluster={r.cluster || cluster}
    apiVersion={apiVersionFor(r)}
    kind={r.kind}
    name={r.name}
    namespace={r.namespace}
    className={chip ? 'ai-ref-chip' : 'ai-ref'}
  >
    <span className="ai-ref-kind">{r.kind}</span>
    {r.namespace && <span className="ai-ref-ns">{r.namespace}/</span>}
    <span className="ai-ref-name">{r.name}</span>
  </ResourceLink>
);

export const Inline: React.FC<{ text: string; cluster: string }> = ({ text, cluster }) => (
  <>
    {parseInline(text).map((p, i) => {
      if (p.type === 'bold') return <strong key={i}>{p.text}</strong>;
      if (p.type === 'code') {
        const ref = refFromCode(p.text);
        return ref ? <Ref key={i} r={ref} cluster={cluster} /> : <code key={i} className="ai-inline-code">{p.text}</code>;
      }
      return <React.Fragment key={i}>{p.text}</React.Fragment>;
    })}
  </>
);

export const Markdown: React.FC<{ md: string; cluster: string }> = ({ md, cluster }) => (
  <div className="ai-md">
    {parseMarkdown(stripPatchBlocks(md)).map((b, i) => {
      switch (b.type) {
        case 'h':
          return <div key={i} className={`ai-md-h ai-md-h${Math.min(b.level, 3)}`}><Inline text={b.text} cluster={cluster} /></div>;
        case 'ul':
          return <ul key={i}>{b.items.map((it, j) => <li key={j}><Inline text={it} cluster={cluster} /></li>)}</ul>;
        case 'ol':
          return <ol key={i}>{b.items.map((it, j) => <li key={j}><Inline text={it} cluster={cluster} /></li>)}</ol>;
        case 'code':
          return <pre key={i} className="ai-code">{b.text}</pre>;
        case 'table':
          return (
            <div key={i} className="ai-md-table-wrap">
              <table className="ai-md-table">
                <thead>
                  <tr>
                    {b.header.map((h, j) => (
                      <th key={j} style={b.align[j] ? { textAlign: b.align[j]! } : undefined}><Inline text={h} cluster={cluster} /></th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {b.rows.map((r, j) => (
                    <tr key={j}>
                      {r.map((c, k) => (
                        <td key={k} style={b.align[k] ? { textAlign: b.align[k]! } : undefined}><Inline text={c} cluster={cluster} /></td>
                      ))}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          );
        case 'quote':
          return <blockquote key={i} className="ai-md-quote"><Inline text={b.text} cluster={cluster} /></blockquote>;
        case 'hr':
          return <hr key={i} className="ai-md-hr" />;
        default:
          return <p key={i}><Inline text={b.text} cluster={cluster} /></p>;
      }
    })}
  </div>
);

export const sevClass = (s?: string) =>
  s === 'critical' ? 'ap-badge--danger' : s === 'warning' ? 'ap-badge--warning' : s === 'healthy' ? 'ap-badge--success' : 'ap-badge--info';

/** One compact tool call: verb, args, live state and duration. */
export const ToolStep: React.FC<{ e: InvestigationEvent; result?: InvestigationEvent; running: boolean; now?: number }> = ({ e, result, running, now }) => {
  const [open, setOpen] = useState(false);
  const state = !result ? (running ? 'running' : 'stopped') : result.ok === false ? 'error' : 'ok';
  const dur = result ? result.time - e.time : running && now ? now - e.time : 0;
  return (
    <div className={`ai-step ai-step--tool is-${state}`}>
      <button type="button" className="ai-step-head" onClick={() => setOpen(!open)} aria-expanded={open}>
        <span className="ai-step-icon">{state === 'running' ? <span className="ai-spinner" /> : <ToolIcon />}</span>
        <span className="ai-step-tool">{toolVerb(e.tool || '', e.input)}</span>
        <span className="ai-step-args ap-truncate">{e.input && e.input !== '{}' ? e.input : ''}</span>
        {dur > 0 && <span className="ai-step-dur">{dur < 1000 ? `${dur}ms` : `${(dur / 1000).toFixed(1)}s`}</span>}
        {(e.input || result?.text) && (open ? <ChevronDownIcon /> : <ChevronRightIcon />)}
      </button>
      {open && (
        <div className="ai-step-detail">
          {e.input && <pre className="ai-code ai-code--sm">{e.input}</pre>}
          {result?.text && <pre className="ai-code ai-code--sm">{result.text}</pre>}
        </div>
      )}
    </div>
  );
};

export const ThinkingStep: React.FC<{ text: string }> = ({ text }) => {
  const [open, setOpen] = useState(false);
  return (
    <div className="ai-step ai-step--thinking">
      <button type="button" className="ai-step-head" onClick={() => setOpen(!open)} aria-expanded={open}>
        <span className="ai-step-icon">{open ? <ChevronDownIcon /> : <ChevronRightIcon />}</span>
        <span className="ai-muted">Thinking</span>
      </button>
      {open && <div className="ai-step-detail ai-muted">{text}</div>}
    </div>
  );
};

/** Structured findings the agent ended with. */
export const FindingsCard: React.FC<{ f: Findings; cluster: string }> = ({ f, cluster }) => (
  <div className="ai-summary-card">
    <div className="ai-summary-top">
      {f.severity && <span className={`ap-badge ${sevClass(f.severity)}`}>{f.severity}</span>}
      {f.confidence && <span className="ap-badge">confidence: {f.confidence}</span>}
    </div>
    <div className="ai-summary-text"><Inline text={f.summary} cluster={cluster} /></div>
    {f.rootCause && (
      <div className="ai-rootcause">
        <div className="ai-label">Root cause</div>
        <Inline text={f.rootCause} cluster={cluster} />
      </div>
    )}
    {f.findings && f.findings.length > 0 && (
      <div className="ai-finding-list">
        {f.findings.map((fd, i) => (
          <div key={i} className="ai-finding">
            <div className="ai-finding-head">
              <span className={`ap-dot ap-dot--${fd.severity === 'critical' ? 'danger' : fd.severity === 'warning' ? 'warning' : 'success'}`} />
              <span className="ai-finding-title"><Inline text={fd.title} cluster={cluster} /></span>
            </div>
            {fd.detail && <div className="ai-finding-detail"><Inline text={fd.detail} cluster={cluster} /></div>}
            {fd.resources && fd.resources.length > 0 && (
              <div className="ai-chips">{fd.resources.map((r, j) => <Ref key={j} r={r} cluster={cluster} chip />)}</div>
            )}
          </div>
        ))}
      </div>
    )}
    {f.nextSteps && f.nextSteps.length > 0 && (
      <div className="ai-rootcause">
        <div className="ai-label">Next steps</div>
        <ol className="ai-next">{f.nextSteps.map((s, i) => <li key={i}><Inline text={s} cluster={cluster} /></li>)}</ol>
      </div>
    )}
  </div>
);

/** A context chip; removable while composing. */
export const ChipView: React.FC<{ c: ContextChip; onRemove?: () => void }> = ({ c, onRemove }) => (
  <span className={`ai-ctx-chip is-${c.type}`} title={c.text ? c.text.slice(0, 400) : c.label}>
    {c.type === 'resource' ? <span className="ai-ctx-kind">{c.kind}</span> : c.type === 'finding' || c.type === 'text' || c.type === 'yaml' ? <SparkleIcon size={10} /> : null}
    <span className="ap-truncate">{c.type === 'resource' ? (c.namespace ? `${c.namespace}/${c.name}` : c.name) : c.label}</span>
    {onRemove && (
      <button type="button" className="ai-ctx-x" onClick={onRemove} aria-label={`Remove ${c.label}`}><Cross2Icon width={10} height={10} /></button>
    )}
  </span>
);
