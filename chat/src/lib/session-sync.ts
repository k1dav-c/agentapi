// How far the client has applied the server's changes: the epoch of the
// conversation and the seq it resumes from on the next connection (the
// watermark). The replay that starts a connection comes grouped by kind
// (messages, then rich messages, then requests), not in seq order, so the
// watermark only moves to the replay's seq once the whole replay arrived:
// the server gives the replay's last event an SSE id. Moving it earlier
// would make a reconnect or a cached reload skip what was still on its way.
export class SyncTracker {
  epoch = "";
  watermark = 0;
  private replayEnd: number | null = null;
  private replayId = "";

  // A session_sync begins a replay of the changes after the watermark, or
  // of everything when it is full or of another epoch. lastEventId is the
  // session_sync event's. It returns whether the client must replace its
  // state with what follows.
  begin(sync: {epoch: string; seq: number; full?: boolean}, lastEventId: string): boolean {
    const full = Boolean(sync.full) || sync.epoch !== this.epoch;
    if (full) {
      this.epoch = sync.epoch;
      this.watermark = 0;
    }
    this.replayEnd = sync.seq > this.watermark ? sync.seq : null;
    this.replayId = lastEventId;
    return full;
  }

  // Whether a replay is still on its way.
  get replaying(): boolean {
    return this.replayEnd !== null;
  }

  // Notes an event's lastEventId. It returns true when the event completes
  // the replay: it is the first to carry an SSE id since the replay began.
  noteEvent(lastEventId: string): boolean {
    if (this.replayEnd === null || lastEventId === this.replayId) return false;
    this.watermark = Math.max(this.watermark, this.replayEnd);
    this.replayEnd = null;
    return true;
  }

  // Records that the changes up to seq were applied. Outside a replay they
  // arrive in order, so the watermark follows; during one it waits.
  applied(seq: number): void {
    if (this.replayEnd === null) this.watermark = Math.max(this.watermark, seq);
  }

  // Resumes from a cached conversation.
  restore(epoch: string, seq: number): void {
    this.epoch = epoch;
    this.watermark = seq;
    this.replayEnd = null;
  }

  // Forgets the conversation: the next connection gets the full state.
  reset(): void {
    this.restore("", 0);
  }
}
