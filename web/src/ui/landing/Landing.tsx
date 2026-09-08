import { useEffect, useRef, useState, type KeyboardEvent } from 'react';
import { useLanguage } from './Language';
import { Signup } from './Signup';

export function Landing() {
  const { t, lang } = useLanguage();
  const [path, setPath] = useState<'relayed' | 'direct'>('relayed');
  const [open, setOpen] = useState<string | null>(null);
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement | null>(null);
  function close() {
    setOpen(null);
    trigger.current?.focus();
  }
  function toggle(id: string, button: HTMLButtonElement) {
    trigger.current = button;
    setOpen(open === id ? null : id);
  }
  useEffect(() => {
    if (!open) return;
    root.current?.querySelector<HTMLElement>(`[id="${open}"][tabindex]`)?.focus({ preventScroll: true });
    function outside(e: MouseEvent) {
      const target = e.target as Element;
      if (!target.closest('.fig, .anat')) setOpen(null);
    }
    document.addEventListener('click', outside);
    return () => document.removeEventListener('click', outside);
  }, [open]);
  function switchPath(e: KeyboardEvent<HTMLButtonElement>) {
    if (!['ArrowRight', 'ArrowDown', 'ArrowLeft', 'ArrowUp'].includes(e.key)) return;
    e.preventDefault();
    const next = path === 'relayed' ? 'direct' : 'relayed';
    setPath(next);
    root.current?.querySelector<HTMLButtonElement>(`button.${next}`)?.focus();
  }
  return (
    <div
      className={`landing is-desk ${lang}`}
      ref={root}
      onKeyDown={(e) => {
        if (e.key === 'Escape' && open) {
          e.stopPropagation();
          close();
        }
      }}
    >
      <section className="s" id="p-s1">
        <div className="s-in">
          <div className="s-grid">
            <div className="st">
              <p className="eyebrow" data-copy="s1_eye" dangerouslySetInnerHTML={{ __html: t.s1_eye }} />
              <h2 className="h2" data-copy="s1_h" dangerouslySetInnerHTML={{ __html: t.s1_h }} />
              <p className="lead2" data-copy="s1_lead" dangerouslySetInnerHTML={{ __html: t.s1_lead }} />
              <p className="mut" data-copy="s1_mut" dangerouslySetInnerHTML={{ __html: t.s1_mut }} />
            </div>
            <div className="art">
              <div className="fig" id="p-fig" data-path={path}>
                <div className="fig-c">
                  <button
                    type="button"
                    className="node n-f"
                    aria-expanded={open === 'p-pop-f'}
                    aria-controls="p-pop-f"
                    onClick={(e) => toggle('p-pop-f', e.currentTarget)}
                  >
                    <b data-copy="node_f" dangerouslySetInnerHTML={{ __html: t.node_f }} />
                    <span data-copy="node_f_s" dangerouslySetInnerHTML={{ __html: t.node_f_s }} />
                    <i aria-hidden="true"></i>
                  </button>
                  <svg className="leg l1" aria-hidden="true">
                    <line className="horizontal" x1="0" y1="50%" x2="100%" y2="50%" />
                    <line className="vertical" x1="50%" y1="0" x2="50%" y2="100%" />
                  </svg>
                  <button
                    type="button"
                    className="node n-r"
                    aria-expanded={open === 'p-pop-r'}
                    aria-controls="p-pop-r"
                    onClick={(e) => toggle('p-pop-r', e.currentTarget)}
                  >
                    <b data-copy="node_r" dangerouslySetInnerHTML={{ __html: t.node_r }} />
                    <span data-copy="node_r_s" dangerouslySetInnerHTML={{ __html: t.node_r_s }} />
                    <i aria-hidden="true"></i>
                  </button>
                  <svg className="leg l2" aria-hidden="true">
                    <line className="horizontal" x1="0" y1="50%" x2="100%" y2="50%" />
                    <line className="vertical" x1="50%" y1="0" x2="50%" y2="100%" />
                  </svg>
                  <button
                    type="button"
                    className="node n-h"
                    aria-expanded={open === 'p-pop-h'}
                    aria-controls="p-pop-h"
                    onClick={(e) => toggle('p-pop-h', e.currentTarget)}
                  >
                    <b data-copy="node_h" dangerouslySetInnerHTML={{ __html: t.node_h }} />
                    <span data-copy="node_h_s" dangerouslySetInnerHTML={{ __html: t.node_h_s }} />
                    <i aria-hidden="true"></i>
                  </button>
                  <svg className="brk" viewBox="0 0 100 100" preserveAspectRatio="none" aria-hidden="true">
                    <path
                      className="horizontal"
                      d="M 0 0 V 100 H 100 V 0"
                      vectorEffect="non-scaling-stroke"
                    />
                    <path className="vertical" d="M 0 0 H 100 V 100 H 0" vectorEffect="non-scaling-stroke" />
                  </svg>
                  <p className="brk-l" data-copy="brk_l" dangerouslySetInnerHTML={{ __html: t.brk_l }} />
                </div>
                <div className="pops">
                  <div
                    className="pop p-f"
                    id="p-pop-f"
                    role="region"
                    tabIndex={-1}
                    data-copy-aria="node_f"
                    aria-label={t.node_f}
                    hidden={open !== 'p-pop-f'}
                  >
                    <h4>
                      <span data-copy="node_f" dangerouslySetInnerHTML={{ __html: t.node_f }} />
                      <button
                        type="button"
                        className="x"
                        onClick={close}
                        data-copy="close"
                        dangerouslySetInnerHTML={{ __html: t.close }}
                      />
                    </h4>
                    <div data-copy="pop_f" dangerouslySetInnerHTML={{ __html: t.pop_f }} />
                  </div>
                  <div
                    className="pop p-r"
                    id="p-pop-r"
                    role="region"
                    tabIndex={-1}
                    data-copy-aria="node_r"
                    aria-label={t.node_r}
                    hidden={open !== 'p-pop-r'}
                  >
                    <h4>
                      <span data-copy="node_r" dangerouslySetInnerHTML={{ __html: t.node_r }} />
                      <button
                        type="button"
                        className="x"
                        onClick={close}
                        data-copy="close"
                        dangerouslySetInnerHTML={{ __html: t.close }}
                      />
                    </h4>
                    <div data-copy="pop_r" dangerouslySetInnerHTML={{ __html: t.pop_r }} />
                  </div>
                  <div
                    className="pop p-h"
                    id="p-pop-h"
                    role="region"
                    tabIndex={-1}
                    data-copy-aria="node_h"
                    aria-label={t.node_h}
                    hidden={open !== 'p-pop-h'}
                  >
                    <h4>
                      <span data-copy="node_h" dangerouslySetInnerHTML={{ __html: t.node_h }} />
                      <button
                        type="button"
                        className="x"
                        onClick={close}
                        data-copy="close"
                        dangerouslySetInnerHTML={{ __html: t.close }}
                      />
                    </h4>
                    <div data-copy="pop_h" dangerouslySetInnerHTML={{ __html: t.pop_h }} />
                  </div>
                </div>
              </div>
              <div className="pathrow">
                <div
                  className="seg-ctl"
                  role="radiogroup"
                  data-copy-aria="path_label"
                  aria-label={t.path_label}
                >
                  <button
                    type="button"
                    role="radio"
                    className="relayed"
                    aria-checked={path === 'relayed'}
                    tabIndex={path === 'relayed' ? 0 : -1}
                    onClick={() => setPath('relayed')}
                    onKeyDown={switchPath}
                  >
                    <span className="dot"></span>
                    <span data-copy="path_r" dangerouslySetInnerHTML={{ __html: t.path_r }} />
                  </button>
                  <button
                    type="button"
                    role="radio"
                    className="direct"
                    aria-checked={path === 'direct'}
                    tabIndex={path === 'direct' ? 0 : -1}
                    onClick={() => setPath('direct')}
                    onKeyDown={switchPath}
                  >
                    <span className="dot"></span>
                    <span data-copy="path_d" dangerouslySetInnerHTML={{ __html: t.path_d }} />
                  </button>
                </div>
                <p
                  className="path-d"
                  data-path-d="relayed"
                  data-copy="path_r_d"
                  dangerouslySetInnerHTML={{ __html: t.path_r_d }}
                />
                <p
                  className="path-d"
                  data-path-d="direct"
                  hidden={path !== 'direct'}
                  data-copy="path_d_d"
                  dangerouslySetInnerHTML={{ __html: t.path_d_d }}
                />
              </div>
              <div className="facts3">
                <div className="fact">
                  <p className="k" data-copy="fact1_k" dangerouslySetInnerHTML={{ __html: t.fact1_k }} />
                  <p className="v" data-copy="fact1_v" dangerouslySetInnerHTML={{ __html: t.fact1_v }} />
                </div>
                <div className="fact">
                  <p className="k" data-copy="fact2_k" dangerouslySetInnerHTML={{ __html: t.fact2_k }} />
                  <p className="v" data-copy="fact2_v" dangerouslySetInnerHTML={{ __html: t.fact2_v }} />
                </div>
                <div className="fact">
                  <p className="k" data-copy="fact3_k" dangerouslySetInnerHTML={{ __html: t.fact3_k }} />
                  <p className="v" data-copy="fact3_v" dangerouslySetInnerHTML={{ __html: t.fact3_v }} />
                </div>
              </div>
            </div>
          </div>
        </div>
      </section>
      <section className="s" id="p-s2">
        <div className="s-in">
          <div className="s-grid">
            <div className="st">
              <p className="eyebrow" data-copy="s2_eye" dangerouslySetInnerHTML={{ __html: t.s2_eye }} />
              <h2 className="h2" data-copy="s2_h" dangerouslySetInnerHTML={{ __html: t.s2_h }} />
              <p className="lead2" data-copy="s2_lead" dangerouslySetInnerHTML={{ __html: t.s2_lead }} />
              <p className="mut" data-copy="s2_mut" dangerouslySetInnerHTML={{ __html: t.s2_mut }} />
            </div>
            <div className="art">
              <div className="anat" id="p-anat">
                <button
                  type="button"
                  className="seg v"
                  aria-describedby="p-tt-v"
                  aria-expanded={open === 'p-tt-v'}
                  onClick={(e) => toggle('p-tt-v', e.currentTarget)}
                >
                  {'ic1'}
                  <span className="n" data-copy="seg_v" dangerouslySetInnerHTML={{ __html: t.seg_v }} />
                </button>
                <span className="dot">{'.'}</span>
                <button
                  type="button"
                  className="seg a"
                  aria-describedby="p-tt-a"
                  aria-expanded={open === 'p-tt-a'}
                  onClick={(e) => toggle('p-tt-a', e.currentTarget)}
                >
                  {'tco2Fw8yq3zn'}
                  <span className="n" data-copy="seg_a" dangerouslySetInnerHTML={{ __html: t.seg_a }} />
                </button>
                <span className="dot">{'.'}</span>
                <button
                  type="button"
                  className="seg s"
                  aria-describedby="p-tt-s"
                  aria-expanded={open === 'p-tt-s'}
                  onClick={(e) => toggle('p-tt-s', e.currentTarget)}
                >
                  {'rrMAuKdLMEAcv3ODDkyCMa07oXrpwKXc4MDOr4C7nNU'}
                  <span className="n" data-copy="seg_s" dangerouslySetInnerHTML={{ __html: t.seg_s }} />
                </button>
                <div className="tips">
                  <div
                    className="tip t-v"
                    id="p-tt-v"
                    role="tooltip"
                    hidden={open !== 'p-tt-v'}
                    data-copy="tt_v"
                    dangerouslySetInnerHTML={{ __html: t.tt_v }}
                  />
                  <div
                    className="tip t-a"
                    id="p-tt-a"
                    role="tooltip"
                    hidden={open !== 'p-tt-a'}
                    data-copy="tt_a"
                    dangerouslySetInnerHTML={{ __html: t.tt_a }}
                  />
                  <div
                    className="tip t-s"
                    id="p-tt-s"
                    role="tooltip"
                    hidden={open !== 'p-tt-s'}
                    data-copy="tt_s"
                    dangerouslySetInnerHTML={{ __html: t.tt_s }}
                  />
                </div>
              </div>
              <p className="verbs" data-copy="verbs" dangerouslySetInnerHTML={{ __html: t.verbs }} />
              <details className="dis">
                <summary data-copy="lim_sum" dangerouslySetInnerHTML={{ __html: t.lim_sum }} />
                <div className="dis-b">
                  <div className="defs">
                    <div className="def">
                      <div className="def-v">{'20'}</div>
                      <div className="def-l" data-copy="d1_l" dangerouslySetInnerHTML={{ __html: t.d1_l }} />
                    </div>
                    <div className="def">
                      <div className="def-v">{'20 000'}</div>
                      <div className="def-l" data-copy="d2_l" dangerouslySetInnerHTML={{ __html: t.d2_l }} />
                    </div>
                    <div className="def">
                      <div className="def-v">{'1'}</div>
                      <div className="def-l" data-copy="d3_l" dangerouslySetInnerHTML={{ __html: t.d3_l }} />
                    </div>
                    <div className="def">
                      <div className="def-v">{'200 000'}</div>
                      <div className="def-l" data-copy="d4_l" dangerouslySetInnerHTML={{ __html: t.d4_l }} />
                    </div>
                    <div className="def">
                      <div className="def-v" data-copy="d5_v" dangerouslySetInnerHTML={{ __html: t.d5_v }} />
                      <div className="def-l" data-copy="d5_l" dangerouslySetInnerHTML={{ __html: t.d5_l }} />
                    </div>
                    <div className="def">
                      <div className="def-v" data-copy="d6_v" dangerouslySetInnerHTML={{ __html: t.d6_v }} />
                      <div className="def-l" data-copy="d6_l" dangerouslySetInnerHTML={{ __html: t.d6_l }} />
                    </div>
                  </div>
                  <p data-copy="lim_note" dangerouslySetInnerHTML={{ __html: t.lim_note }} />
                </div>
              </details>
            </div>
          </div>
        </div>
      </section>
      <section className="s" id="p-s3">
        <div className="s-in">
          <div className="s-grid">
            <div className="st">
              <p className="eyebrow">
                <span data-copy="s3_eye" dangerouslySetInnerHTML={{ __html: t.s3_eye }} />
              </p>
              <h2 className="h2" data-copy="s3_h" dangerouslySetInnerHTML={{ __html: t.s3_h }} />
              <p className="lead2" data-copy="s3_lead" dangerouslySetInnerHTML={{ __html: t.s3_lead }} />
              <p className="mut" data-copy="s3_mut" dangerouslySetInnerHTML={{ __html: t.s3_mut }} />
            </div>
            <div className="art">
              <pre className="code" data-copy="terminal">
                {t.terminal}
              </pre>
              <details className="dis">
                <summary data-copy="reach_sum" dangerouslySetInnerHTML={{ __html: t.reach_sum }} />
                <div className="dis-b" data-copy="reach_b" dangerouslySetInnerHTML={{ __html: t.reach_b }} />
              </details>
            </div>
          </div>
        </div>
      </section>
      <section className="s" id="p-s4">
        <div className="s-in">
          <div className="s-grid">
            <div className="st">
              <p className="eyebrow">
                <span data-copy="s4_eye" dangerouslySetInnerHTML={{ __html: t.s4_eye }} />
                <span
                  className="tag warn"
                  data-copy="s4_tag"
                  dangerouslySetInnerHTML={{ __html: t.s4_tag }}
                />
              </p>
              <h2 className="h2" data-copy="s4_h" dangerouslySetInnerHTML={{ __html: t.s4_h }} />
              <p className="price" data-copy="s4_price" dangerouslySetInnerHTML={{ __html: t.s4_price }} />
              <p className="lead2" data-copy="s4_lead" dangerouslySetInnerHTML={{ __html: t.s4_lead }} />
              <p className="mut" data-copy="s4_mut" dangerouslySetInnerHTML={{ __html: t.s4_mut }} />
            </div>
            <div className="art">
              <div className="rm" role="img" data-copy-aria="roadmap_label" aria-label={t.roadmap_label}>
                <div className="bx">
                  <span data-copy="rm_c" dangerouslySetInnerHTML={{ __html: t.rm_c }} />
                </div>
                <svg className="cn" aria-hidden="true">
                  <line className="horizontal" x1="0" y1="50%" x2="100%" y2="50%" />
                  <line className="vertical" x1="50%" y1="0" x2="50%" y2="100%" />
                </svg>
                <div className="bx dash">
                  <span data-copy="rm_g" dangerouslySetInnerHTML={{ __html: t.rm_g }} />
                  <span className="u" data-copy="rm_g_s" dangerouslySetInnerHTML={{ __html: t.rm_g_s }} />
                </div>
                <svg className="cn" aria-hidden="true">
                  <line className="horizontal" x1="0" y1="50%" x2="100%" y2="50%" />
                  <line className="vertical" x1="50%" y1="0" x2="50%" y2="100%" />
                </svg>
                <div className="bx">
                  <span data-copy="rm_h" dangerouslySetInnerHTML={{ __html: t.rm_h }} />
                </div>
              </div>
              <Signup />
            </div>
          </div>
        </div>
      </section>
    </div>
  );
}
