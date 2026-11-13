// 仕様のページの部品。json-render を使う版と、使わない版の両方が、この同じ部品を使う。
import { Fragment, useState, type ReactNode } from "react";
import type { z } from "zod";
import type * as P from "./schema.ts";

type Head = z.infer<typeof P.pageHead>;
type StoryP = z.infer<typeof P.story>;
type PromisesP = z.infer<typeof P.promises>;
type ExplorerP = z.infer<typeof P.explorer>;
type QuizP = z.infer<typeof P.quiz>;
type LimitsP = z.infer<typeof P.limits>;
type Node = ExplorerP["nodes"][number];

export function PageShell({ head, children }: { head: Head; children: ReactNode }) {
  return (
    <div className="page">
      <header>
        <p className="kicker">{head.spec}</p>
        <h1>{head.title}</h1>
        <p className="lead">{head.lead}</p>
      </header>
      {children}
    </div>
  );
}

export function Story({ p }: { p: StoryP }) {
  return (
    <section id="story">
      <h2>{p.heading}</h2>
      <p className="sub">{p.note}</p>
      <ol className="story" style={{ ["--cols" as string]: p.steps.length }}>
        {p.steps.map((s, i) => (
          <li key={i} className={s.focus ? "here" : undefined}>
            <p className="who">
              <span className="num">{i + 1}</span>
              {s.who}
              {s.focus && <span className="here-tag">この仕様の話</span>}
            </p>
            {s.cardText && (
              <div className="doc">
                {s.cardLabel && <p className="id">{s.cardLabel}</p>}
                <p>{s.cardText}</p>
              </div>
            )}
            {s.choices && (
              <ul className="pick">
                {s.choices.map((c, j) => (
                  <li key={j} className={c.picked ? "sel" : undefined}>
                    {c.text}
                    {c.picked && <span className="sel-tag">選んだ</span>}
                  </li>
                ))}
              </ul>
            )}
            <p className="cap">{s.caption}</p>
          </li>
        ))}
      </ol>
    </section>
  );
}

export function Promises({ p }: { p: PromisesP }) {
  return (
    <section>
      <h2>{p.heading}</h2>
      <div className="risks" style={{ ["--cols" as string]: Math.min(p.items.length, 3) }}>
        {p.items.map((it, i) => (
          <div className="risk" key={i}>
            <p className="what">{it.risk}</p>
            <p className="then">{it.why}</p>
            {it.promises.map((v) => (
              <p className="vow" key={v.no}>
                <b>約束 {v.no}</b>
                {v.text}
                <span className="claimref">仕様 {v.claim}</span>
              </p>
            ))}
          </div>
        ))}
      </div>
    </section>
  );
}

// 流れの図。箱の並びから、置き場所を決めて描く。
function Flow({ nodes, path, stoppedAt }: { nodes: Node[]; path: string[]; stoppedAt: string | null }) {
  const sideIds = new Set(nodes.filter((n) => n.branchTo).map((n) => n.branchTo as string));
  const trunk = nodes.filter((n) => !sideIds.has(n.id));
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const idx = new Map(trunk.map((n, i) => [n.id, i]));
  const ROW = 66;
  const TOP = 18;
  const H = 36;
  const TX = 16;
  const TW = 236;
  const SX = 296;
  const SW = 192;
  const y = (i: number) => TOP + i * ROW;
  const onNode = new Set(path);
  const trunkInPath = path.map((id) => idx.get(id)).filter((v): v is number => v !== undefined);
  const edgeOn = (i: number) => {
    for (let k = 0; k + 1 < trunkInPath.length; k++) {
      if (trunkInPath[k] <= i && trunkInPath[k + 1] >= i + 1) return true;
    }
    return false;
  };
  const branchOn = (n: Node) => {
    const k = path.indexOf(n.id);
    return k >= 0 && path[k + 1] === n.branchTo;
  };
  const other = (a: string) => (a === "はい" ? "いいえ" : "はい");
  const height = y(trunk.length - 1) + H + 14;
  const fit = (text: string, width: number) =>
    text.length * 12.5 > width - 16 ? { textLength: width - 16, lengthAdjust: "spacingAndGlyphs" as const } : {};
  const box = (n: Node, x: number, yy: number, w: number, key: string) => {
    const cls = ["n", n.kind === "end" ? "end" : "", onNode.has(n.id) ? "on" : "", stoppedAt === n.id ? "halt" : ""]
      .filter(Boolean)
      .join(" ");
    const rx = n.kind === "check" ? 18 : n.kind === "end" ? 2 : 4;
    return (
      <g className={cls} key={key}>
        <rect x={x} y={yy} width={w} height={H} rx={rx} />
        {n.kind === "end" && <rect className="bar" x={x} y={yy} width={5} height={H} />}
        <text x={x + w / 2 + (n.kind === "end" ? 2 : 0)} y={yy + 23} {...fit(n.text, w)}>
          {n.text}
        </text>
        {n.note && (
          <text className="lbl s" x={x + 2} y={yy - 5}>
            {n.note}
          </text>
        )}
      </g>
    );
  };
  return (
    <svg className="flow" viewBox={`0 0 500 ${height}`} role="img" aria-label="流れの図。上から順に確かめ、答えによって横の終わり方へ分かれる。">
      <defs>
        <marker id="ah" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto">
          <path className="ah" d="M0,0 L8,4 L0,8 z" />
        </marker>
        <marker id="ah-on" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="5" markerHeight="5" orient="auto">
          <path className="ah-on" d="M0,0 L8,4 L0,8 z" />
        </marker>
      </defs>
      {trunk.slice(0, -1).map((n, i) => {
        const on = edgeOn(i);
        const gap = trunk[i + 1].note ? 12 : 0;
        return (
          <g className={"e" + (on ? " on" : "")} key={"t" + n.id}>
            <line
              x1={TX + TW / 2}
              y1={y(i) + H}
              x2={TX + TW / 2}
              y2={y(i + 1) - gap}
              markerEnd={on ? "url(#ah-on)" : "url(#ah)"}
            />
            {n.kind === "check" && n.branchOn && (
              <text className="lbl s" x={TX + TW / 2 + 8} y={y(i) + H + 16}>
                {other(n.branchOn)}
              </text>
            )}
          </g>
        );
      })}
      {trunk.map((n, i) => {
        if (!n.branchTo) return null;
        const on = branchOn(n);
        return (
          <g className={"e" + (on ? " on" : "")} key={"b" + n.id}>
            <line x1={TX + TW} y1={y(i) + H / 2} x2={SX} y2={y(i) + H / 2} markerEnd={on ? "url(#ah-on)" : "url(#ah)"} />
            <text className="lbl" x={(TX + TW + SX) / 2} y={y(i) + H / 2 - 6}>
              {n.branchOn}
            </text>
          </g>
        );
      })}
      {trunk.map((n, i) => box(n, TX, y(i), TW, "t" + i))}
      {trunk.map((n, i) => {
        const side = n.branchTo ? byId.get(n.branchTo) : undefined;
        return side ? box(side, SX, y(i), SW, "s" + i) : null;
      })}
    </svg>
  );
}

export function Explorer({
  p,
  scene,
  onScene,
}: {
  p: ExplorerP;
  scene: string | null;
  onScene: (id: string) => void;
}) {
  const cur = p.scenes.find((s) => s.id === scene) ?? p.scenes.find((s) => s.usual) ?? p.scenes[0];
  const byId = new Map(p.nodes.map((n) => [n.id, n]));
  return (
    <section id="run">
      <h2>{p.heading}</h2>
      <p className="sub">{p.prompt}</p>
      <div className="grid">
        <div className="panel" id="inputs">
          <h3>途中で起きること（一つ選ぶ）</h3>
          <div className="presets">
            {p.scenes.map((s) => (
              <button
                type="button"
                className="preset"
                key={s.id}
                aria-pressed={s.id === cur.id}
                onClick={() => onScene(s.id)}
              >
                <span className="t">
                  {s.title}
                  {s.usual && <span className="real">ふつうはこれ</span>}
                </span>
                <span className="d">{s.desc}</span>
              </button>
            ))}
          </div>
        </div>
        <div className="panel" aria-live="polite">
          <div className="verdict-box">
            <p className="verdict-kind">結果</p>
            <p className="verdict">{cur.verdict}</p>
            <p className="because">{cur.because}</p>
          </div>
        </div>
        <div className="panel">
          <h3>起きたこと（上から順に）</h3>
          <ol className="steps">
            {cur.events.map((e, i) => (
              <li key={i} className={e.checks ? "focus" : undefined}>
                <div>
                  <span className="act">{e.text}</span>
                  {e.detail && <span className="res">{e.detail}</span>}
                  {e.checks && (
                    <ul className="checks">
                      {e.checks.map((c, j) => (
                        <li key={j}>
                          <span>{byId.get(c.node)?.text ?? c.node}</span>
                          <span className={"ans " + c.tone}>{c.answer}</span>
                          {c.why && <span className="why">{c.why}</span>}
                        </li>
                      ))}
                    </ul>
                  )}
                  {e.outcome && <p className="outcome">→ {e.outcome}</p>}
                </div>
              </li>
            ))}
          </ol>
        </div>
        <figure className="panel flowfig">
          <p className="figtitle">{p.flowTitle}</p>
          <div className="legend">
            <span>
              <i />
              今選んでいる場面で通った道
            </span>
            <span>
              <i className="h" />
              止まった所
            </span>
          </div>
          <div className="flowwrap">
            <Flow nodes={p.nodes} path={cur.path} stoppedAt={cur.stoppedAt} />
          </div>
        </figure>
        <div className="panel">
          <h3>終わった後に、残っているもの</h3>
          <dl className="state">
            {cur.after.map((a, i) => (
              <Fragment key={i}>
                <dt>{a.label}</dt>
                <dd className={a.bad ? "wrong" : undefined}>{a.value}</dd>
              </Fragment>
            ))}
          </dl>
        </div>
      </div>
    </section>
  );
}

function QuizItem({ item, no, onScene }: { item: QuizP["items"][number]; no: number; onScene: (id: string) => void }) {
  const [picked, setPicked] = useState<number | null>(null);
  const ok = picked === item.answer;
  return (
    <div className="panel">
      <p className="q">
        問 {no}　{item.q}
      </p>
      {item.options.map((text, i) => (
        <button
          type="button"
          key={i}
          className={"choice" + (picked === i ? (ok ? " right" : " wrong") : "")}
          onClick={() => setPicked(i)}
        >
          {text}
          {picked === i && <span className="mark">{ok ? "正しい" : "違う"}</span>}
        </button>
      ))}
      {picked !== null && <p className="explain">{ok ? item.why : "違う。もう一度選ぶか、下のボタンでこの場面を試す。"}</p>}
      {item.scene && (
        <button
          type="button"
          className="linkbtn"
          onClick={() => {
            onScene(item.scene as string);
            document.getElementById("run")?.scrollIntoView({ behavior: "smooth", block: "start" });
          }}
        >
          この場面を、上で試す
        </button>
      )}
    </div>
  );
}

export function Quiz({ p, onScene }: { p: QuizP; onScene: (id: string) => void }) {
  return (
    <section>
      <h2>{p.heading}</h2>
      <div className="quiz" style={{ ["--cols" as string]: Math.min(p.items.length, 3) }}>
        {p.items.map((item, i) => (
          <QuizItem key={i} item={item} no={i + 1} onScene={onScene} />
        ))}
      </div>
    </section>
  );
}

export function Limits({ p }: { p: LimitsP }) {
  return (
    <section>
      <h2>{p.heading}</h2>
      <div className="limits">
        <div className="ring out">
          <p className="ring-h">仕様の前提</p>
          <ul>{p.assumptions.length ? p.assumptions.map((t, i) => <li key={i}>{t}</li>) : <li>なし</li>}</ul>
        </div>
        <div className="ring out">
          <p className="ring-h">仕様が約束していないこと</p>
          <ul>{p.notPromised.length ? p.notPromised.map((t, i) => <li key={i}>{t}</li>) : <li>なし</li>}</ul>
        </div>
        <div className="ring mid">
          <p className="ring-h">約束に選ばなかった主張</p>
          <ul>
            {p.otherClaims.length ? (
              p.otherClaims.map((c, i) => (
                <li key={i}>
                  {c.text}
                  <span className="claimref">仕様 {c.claim}</span>
                </li>
              ))
            ) : (
              <li>なし</li>
            )}
          </ul>
        </div>
      </div>
    </section>
  );
}
