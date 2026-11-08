import type { VerificationPlugin } from "@prepare-dev/core";
import { Questions, type RecoveryHooks, type RecoveryReport } from "./questions.js";
import { Runs } from "./runs.js";
import { Store } from "./store.js";
import { Trace } from "./trace.js";
import { Status } from "./status.js";

export interface RuntimeOptions {
  plugins?: VerificationPlugin[];
  recoveryHooks?: RecoveryHooks;
}

/**
 * One Runtime per project directory. Opening it loads the journal, rebuilds the index,
 * scans records for external changes and runs recovery before anything else is served.
 */
export class Runtime {
  readonly store: Store;
  readonly questions: Questions;
  readonly runs: Runs;
  readonly trace: Trace;
  readonly status: Status;
  readonly recovery: RecoveryReport;

  private constructor(store: Store, plugins: VerificationPlugin[], recoveryHooks?: RecoveryHooks) {
    this.store = store;
    this.questions = new Questions(store);
    this.recovery = this.questions.recover(recoveryHooks);
    this.runs = new Runs(store, plugins);
    this.trace = new Trace(store, this.questions, this.runs);
    this.status = new Status(store, this.questions, this.runs, this.trace);
  }

  static open(root: string, opts: RuntimeOptions = {}): Runtime {
    const store = Store.open(root);
    return new Runtime(store, opts.plugins ?? [], opts.recoveryHooks);
  }

  get root(): string {
    return this.store.root;
  }
}
