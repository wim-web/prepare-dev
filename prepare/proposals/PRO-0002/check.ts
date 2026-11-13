// 部品の中身どうしのつながりの検査（図の箱の名前、場面の名前など）。
// データの形の検査（zod）では見られないので、どちらの版でも、自分で書く必要がある。
type Section = { type: string; props: any };

export function crossCheck(sections: Section[]): string[] {
  const issues: string[] = [];
  const count = (t: string) => sections.filter((s) => s.type === t).length;
  for (const t of ["Story", "Promises", "Explorer", "Quiz", "Limits"]) {
    if (count(t) > 1) issues.push(`${t} が二つ以上ある`);
  }
  const story = sections.find((s) => s.type === "Story")?.props;
  const promises = sections.find((s) => s.type === "Promises")?.props;
  const ex = sections.find((s) => s.type === "Explorer")?.props;
  const quiz = sections.find((s) => s.type === "Quiz")?.props;

  if (story && story.steps.filter((s: any) => s.focus).length !== 1) {
    issues.push("Story: 「この仕様の話」のコマが、ちょうど一つでない");
  }
  if (promises) {
    const nos = promises.items.flatMap((it: any) => it.promises.map((v: any) => v.no));
    if (new Set(nos).size !== nos.length) issues.push("Promises: 約束の番号が重なっている");
  }
  const sceneIds = new Set<string>();
  if (ex) {
    const byId = new Map<string, any>(ex.nodes.map((n: any) => [n.id, n]));
    if (byId.size !== ex.nodes.length) issues.push("Explorer: 図の箱の id が重なっている");
    if (ex.nodes[0]?.kind !== "start") issues.push("Explorer: 図の先頭が start でない");
    const sideIds = new Set<string>();
    for (const n of ex.nodes) {
      if (n.kind === "check") {
        if (!n.branchOn || !n.branchTo) issues.push(`Explorer: 箱 ${n.id} は check なのに、分かれる答えか行き先が無い`);
        else if (byId.get(n.branchTo)?.kind !== "end") issues.push(`Explorer: 箱 ${n.id} の行き先 ${n.branchTo} が、終わり方の箱でない`);
        else sideIds.add(n.branchTo);
      } else if (n.branchOn || n.branchTo) {
        issues.push(`Explorer: 箱 ${n.id} は check でないのに、分かれ道がある`);
      }
    }
    const trunk = ex.nodes.filter((n: any) => !sideIds.has(n.id));
    const pos = new Map<string, number>(trunk.map((n: any, i: number) => [n.id, i]));
    if (trunk[trunk.length - 1]?.kind !== "end") issues.push("Explorer: 図の一番下が、終わり方の箱でない");
    for (const n of trunk.slice(0, -1)) if (n.kind === "end") issues.push(`Explorer: 終わり方の箱 ${n.id} が、図の途中にある`);
    if (ex.scenes.filter((s: any) => s.usual).length !== 1) issues.push("Explorer: 「ふつうの流れ」の場面が、ちょうど一つでない");
    for (const s of ex.scenes) {
      if (sceneIds.has(s.id)) issues.push(`Explorer: 場面の id ${s.id} が重なっている`);
      sceneIds.add(s.id);
      for (const id of s.path) if (!byId.has(id)) issues.push(`Explorer: 場面 ${s.id} の通った道にある ${id} が、図に無い`);
      if (byId.get(s.path[0])?.kind !== "start") issues.push(`Explorer: 場面 ${s.id} の通った道が、start から始まっていない`);
      const last = byId.get(s.path[s.path.length - 1]);
      if (last && last.kind !== "end") issues.push(`Explorer: 場面 ${s.id} の通った道が、終わり方で終わっていない`);
      for (let k = 0; k + 1 < s.path.length; k++) {
        const a = byId.get(s.path[k]);
        const b = byId.get(s.path[k + 1]);
        if (!a || !b) continue;
        const down = pos.has(a.id) && pos.has(b.id) && (pos.get(b.id) as number) > (pos.get(a.id) as number);
        const side = a.kind === "check" && a.branchTo === b.id;
        if (!down && !side) issues.push(`Explorer: 場面 ${s.id} の通った道で、${a.id} から ${b.id} へは、図の上で進めない`);
      }
      if (s.stoppedAt && !byId.has(s.stoppedAt)) issues.push(`Explorer: 場面 ${s.id} の止まった所 ${s.stoppedAt} が、図に無い`);
      for (const e of s.events) {
        for (const c of e.checks ?? []) {
          if (!byId.has(c.node)) issues.push(`Explorer: 場面 ${s.id} で確かめた箱 ${c.node} が、図に無い`);
        }
      }
    }
  }
  if (quiz) {
    if (!ex) issues.push("Quiz: Explorer が無いのに、Quiz がある");
    quiz.items.forEach((it: any, i: number) => {
      if (it.answer >= it.options.length) issues.push(`Quiz: 問 ${i + 1} の正解の位置が、選択肢の数を超えている`);
      if (it.scene && !sceneIds.has(it.scene)) issues.push(`Quiz: 問 ${i + 1} が指す場面 ${it.scene} が、Explorer に無い`);
    });
  }
  return issues;
}
