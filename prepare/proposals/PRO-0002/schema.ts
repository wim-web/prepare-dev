// 仕様のページで使える部品と、部品に入れるデータの形。
// json-render を使う版と、使わない版の両方が、この同じ定義を使う。
import { z } from "zod";

export const pageHead = z.object({
  spec: z.string().describe("説明する仕様の番号。例: SPEC-0001"),
  title: z.string().describe("ページの題。この仕様が何の話かを、読み手の言葉で言う。20 字ぐらい"),
  lead: z.string().describe("題の下の説明。この仕様が何を決めているかを、2〜3 文で"),
});

export const story = z.object({
  heading: z.string().describe("見出し。例: 何の話か"),
  note: z.string().describe("例の前置き。作り話の例なら、そう断る。一文"),
  steps: z
    .array(
      z.object({
        who: z.string().describe("誰が何をするか。主語つきの短い文。例: AI があなたに質問する"),
        cardLabel: z.string().nullable().describe("そのコマで見せる物の名前。例: 質問、要求の文書。無ければ null"),
        cardText: z.string().nullable().describe("見せる物の中身。一文。無ければ null"),
        choices: z
          .array(z.object({ text: z.string(), picked: z.boolean() }))
          .nullable()
          .describe("人が選ぶコマの時だけ、選択肢と、選んだもの。それ以外は null"),
        caption: z.string().describe("そのコマの補足。一文"),
        focus: z.boolean().describe("この仕様が扱うコマなら true。true は一つだけ"),
      }),
    )
    .min(2)
    .max(5)
    .describe("例を、順に見せるコマ。2〜5 個"),
});

export const promises = z.object({
  heading: z.string().describe("見出し。例: 困ることと、仕様の約束"),
  items: z
    .array(
      z.object({
        risk: z.string().describe("困ること。主語つきの短い文"),
        why: z.string().describe("なぜ困るか。2〜3 文"),
        promises: z
          .array(
            z.object({
              no: z.number().int().positive().describe("約束の通し番号。1 から"),
              text: z.string().describe("約束。仕様の主張を、内部の言葉を使わずに言い直した一文"),
              claim: z.string().describe("元になった仕様の主張の番号。例: C1"),
            }),
          )
          .min(1)
          .max(3),
      }),
    )
    .min(1)
    .max(4),
});

export const explorer = z.object({
  heading: z.string().describe("見出し。例: 試す"),
  prompt: z.string().describe("見出しの下の説明。何を選ぶと、何が出るかを一文で"),
  flowTitle: z.string().describe("流れの図の題。誰が何をした時に、何が確かめられるかを一文で"),
  nodes: z
    .array(
      z.object({
        id: z.string().describe("この箱の名前。英小文字と数字。ほかの箱や場面から、この名前で指す"),
        kind: z
          .enum(["start", "check", "step", "end"])
          .describe("start: 始まり。check: 確かめること（答えで分かれる）。step: すること。end: 終わり方"),
        text: z.string().max(18).describe("箱の中の文。18 字まで。check と step には、①②③… の番号を頭に付ける"),
        branchOn: z
          .enum(["はい", "いいえ"])
          .nullable()
          .describe("kind が check の時、横へ分かれる方の答え。もう一方の答えは、下の箱へ進む。check 以外は null"),
        branchTo: z.string().nullable().describe("横へ分かれた先の、終わり方の箱の id。check 以外は null"),
        note: z.string().nullable().describe("箱の上に出す小さな注。例: 途中で落ちた時だけ。無ければ null"),
      }),
    )
    .min(3)
    .max(14)
    .describe(
      "流れの図の箱。上から下へ進む順に並べる。先頭は start、最後は end。横へ分かれた先の end は、最後の end の後ろに並べる",
    ),
  scenes: z
    .array(
      z.object({
        id: z.string().describe("この場面の名前。英小文字と数字"),
        title: z.string().describe("途中で起きること。短い文。例: 答えた後に、文書が書き直された"),
        desc: z.string().describe("その補足。一文"),
        usual: z.boolean().describe("ふつうの流れなら true。true は一つだけ"),
        verdict: z.string().describe("結果の見出し。短い文。例: 決定にしなかった"),
        because: z.string().describe("そうなる理由。2〜3 文"),
        events: z
          .array(
            z.object({
              text: z.string().describe("起きたこと。主語つきの短い文"),
              detail: z.string().nullable().describe("補足。無ければ null"),
              checks: z
                .array(
                  z.object({
                    node: z.string().describe("確かめた箱の id（nodes にあるもの）"),
                    answer: z.string().describe("答え。例: はい、いいえ、終えた"),
                    tone: z
                      .enum(["no", "hit", "done", "halt"])
                      .describe("no: 何もなく下へ進んだ。hit: ここで分かれた。done: やり終えた。halt: ここで止まった"),
                    why: z.string().nullable().describe("その答えになる理由。無ければ null"),
                  }),
                )
                .nullable()
                .describe("この出来事の中で、図の箱を確かめた時だけ。それ以外は null"),
              outcome: z.string().nullable().describe("この出来事の結果。無ければ null"),
            }),
          )
          .min(1)
          .max(8)
          .describe("起きたことを、上から順に"),
        after: z
          .array(
            z.object({
              label: z.string().describe("後に残る物の名前"),
              value: z.string().describe("その状態"),
              bad: z.boolean().describe("あってはならない状態なら true"),
            }),
          )
          .min(1)
          .max(6)
          .describe("終わった後に、残っているもの"),
        path: z.array(z.string()).min(2).describe("この場面で通った箱の id を、通った順に。先頭は start、最後は end"),
        stoppedAt: z.string().nullable().describe("途中で止まった箱の id。止まらなければ null"),
      }),
    )
    .min(2)
    .max(6)
    .describe("人が選べる場面。2〜6 個。結果は、仕様の本文から導けるものだけを書く"),
});

export const quiz = z.object({
  heading: z.string().describe("見出し。例: 分かったか確かめる"),
  items: z
    .array(
      z.object({
        q: z.string().describe("問い。具体的な場面で聞く"),
        options: z.array(z.string()).min(2).max(4).describe("選択肢"),
        answer: z.number().int().min(0).describe("正解の選択肢の位置。0 から数える"),
        why: z.string().describe("正解の理由。どの約束によるかを書く"),
        scene: z.string().nullable().describe("この問いが扱っている場面の id（Explorer の scenes にあるもの）。無ければ null"),
      }),
    )
    .min(1)
    .max(4),
});

export const limits = z.object({
  heading: z.string().describe("見出し。例: この説明が扱っていないこと"),
  assumptions: z.array(z.string()).describe("仕様の前提。読み手の言葉で言い直す"),
  notPromised: z.array(z.string()).describe("仕様が約束していないこと。読み手の言葉で言い直す"),
  otherClaims: z
    .array(z.object({ claim: z.string(), text: z.string() }))
    .describe("約束に選ばなかった主張。番号と、読み手の言葉で言い直した一文"),
});

export const parts = { Story: story, Promises: promises, Explorer: explorer, Quiz: quiz, Limits: limits } as const;

export const descriptions = {
  Page: "ページ全体。一番上に一つだけ置く。中に、下の部品を上から順に並べる。",
  Story: "何の話かが分かる例。仕様が扱う場面を、具体的な一件で順に見せる。ページに一つ。",
  Promises: "困ることと、それに対する仕様の約束を、並べて見せるカード。ページに一つ。",
  Explorer:
    "流れの図と、試せる画面。人が場面を選ぶと、その時に起きることと結果が出て、図の上で通った道に色が付く。仕様に、起きることや入力で結果が変わる決まりがある時だけ使う。ページに一つまで。",
  Quiz: "理解の確認。問いに答えると、正誤と理由が出る。Explorer がある時だけ使う。ページに一つまで。",
  Limits: "この説明が扱っていないこと（仕様の前提、約束していないこと、約束に選ばなかった主張）。ページに一つ。",
} as const;

// 文の書き方の決まり。両方の版で、同じものを AI に渡す。
export const RULES = [
  "読み手は、この仕様の本文を読んでいない開発者。仕様の中の言葉（識別子、状態の名前、関数の名前、英語の用語）と記録の番号を、説明なしに使わない。使ってよい番号は、約束に付ける主張の番号（claim）と、ページの spec だけ。",
  "文には主語を書く（誰が、何をする）。受け身だけで済ませない。",
  "同じものは、同じ言葉で呼ぶ。一つの言葉を、二つの意味で使わない。",
  "例は、仕様が扱う場面が一目で分かる、具体的な一件にする。仕様に例が無ければ、作り話と断って、簡単な例を作る。",
  "場面の結果は、仕様の本文から導けるものだけを書く。仕様に書かれていない結果を作らない。",
  "仕様に、起きることや入力で結果が変わる決まりが無ければ、Explorer と Quiz は使わない。",
  "文体は「だ・である」ではなく、体言止めか「〜する」「〜した」で短く書く。",
];
