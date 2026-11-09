// Load is a read that is under way, went through, or failed.
export type Load<T> = {state: "loading"} | {state: "ready"; value: T} | {state: "failed"; error: string};
