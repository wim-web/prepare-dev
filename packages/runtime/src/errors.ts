export type ErrorCode =
  | "not_found"
  | "conflict"
  | "invalid"
  | "stale"
  | "closed"
  | "inflight"
  | "question_rev"
  | "forbidden"
  | "dead"
  | "unsupported";

export class RuntimeError extends Error {
  readonly code: ErrorCode;
  readonly details: Record<string, unknown>;
  constructor(code: ErrorCode, message: string, details: Record<string, unknown> = {}) {
    super(message);
    this.name = "RuntimeError";
    this.code = code;
    this.details = details;
  }
  get httpStatus(): number {
    switch (this.code) {
      case "not_found":
        return 404;
      case "conflict":
      case "stale":
      case "closed":
      case "inflight":
      case "question_rev":
        return 409;
      case "invalid":
        return 400;
      case "forbidden":
        return 403;
      case "dead":
        return 503;
      case "unsupported":
        return 501;
    }
  }
}

/** Thrown by test hooks to simulate the daemon dying between persistence stages. */
export class SimulatedCrash extends Error {
  readonly stage: string;
  constructor(stage: string) {
    super(`simulated crash after stage ${stage}`);
    this.name = "SimulatedCrash";
    this.stage = stage;
  }
}
