import {describe, expect, test} from "bun:test";
import {EventSourceConnection, QUIET_RECONNECT_MS, type ConnectionOptions, type ConnectionState} from "./event-source";
import {fakeEnvironment} from "./fake-event-source";

function connect(options: Partial<ConnectionOptions> = {}) {
  const env = fakeEnvironment();
  const states: ConnectionState[] = [];
  const events: string[] = [];
  let errors = 0;
  const connection = new EventSourceConnection(
    {
      url: () => "http://agentapi.test/events",
      listeners: () => ({update: (event) => events.push(event.data)}),
      onChange: (state) => states.push(state),
      onError: () => errors++,
      ...options,
    },
    env,
  );
  connection.start();
  return {env, connection, states, events, errors: () => errors};
}

describe("EventSourceConnection", () => {
  test("opens the stream and passes its events on", () => {
    const {env, connection, events} = connect({withCredentials: true});
    expect(env.sources).toHaveLength(1);
    expect(env.latest().init).toEqual({withCredentials: true});
    env.latest().open();
    env.latest().emit("update", "hello");
    expect(connection.current.status).toBe("connected");
    expect(events).toEqual(['"hello"']);
  });

  test("a stream that never opened (a 502, a 401) is opened again after the backoff, not left closed", () => {
    const {env, connection, errors} = connect();
    env.latest().fail();
    expect(env.sources[0].closed).toBe(true);
    expect(connection.current).toMatchObject({status: "reconnecting", attempt: 1});
    expect(connection.current.nextAttemptAt).toBeGreaterThan(env.clock.now());
    expect(errors()).toBe(1);
    env.clock.advance(1200);
    expect(env.sources).toHaveLength(2);
    expect(connection.current.nextAttemptAt).toBeNull();
    env.latest().fail();
    expect(connection.current.attempt).toBe(2);
    env.clock.advance(2400);
    env.latest().open();
    expect(connection.current).toEqual({status: "connected", attempt: 0, nextAttemptAt: null});
  });

  test("a stream that was open and ends comes back at once, without showing unless it takes long", () => {
    const {env, connection, states, errors} = connect();
    env.latest().open();
    const shown = states.length;
    env.latest().fail();
    expect(env.sources).toHaveLength(2);
    expect(connection.current.status).toBe("connected");
    expect(states).toHaveLength(shown);
    expect(errors()).toBe(0);
    env.clock.advance(QUIET_RECONNECT_MS);
    expect(connection.current.status).toBe("reconnecting");
    env.latest().open();
    expect(connection.current.status).toBe("connected");
  });

  test("the watchdog opens a stream that went quiet again", () => {
    const {env, connection} = connect({staleAfter: 45_000});
    env.latest().open();
    // Heartbeats keep it alive.
    for (let i = 0; i < 4; i++) {
      env.clock.advance(15_000);
      env.latest().emit("heartbeat");
    }
    expect(env.sources).toHaveLength(1);
    env.clock.advance(50_000);
    expect(env.sources[0].closed).toBe(true);
    expect(env.sources).toHaveLength(2);
    // Quietly, as the stream was open.
    expect(connection.current.status).toBe("connected");
  });

  test("the watchdog also gives up on a stream that hangs before it opens", () => {
    const {env, connection} = connect({staleAfter: 45_000});
    env.clock.advance(50_000);
    expect(env.sources[0].closed).toBe(true);
    expect(connection.current).toMatchObject({status: "reconnecting", attempt: 1});
  });

  test("a stream without heartbeats has no watchdog", () => {
    const {env} = connect();
    env.latest().open();
    env.clock.advance(10 * 60_000);
    expect(env.sources).toHaveLength(1);
  });

  test("offline closes the stream and waits; online opens it at once", () => {
    const {env, connection} = connect();
    env.latest().open();
    env.setOnline(false);
    expect(env.sources[0].closed).toBe(true);
    expect(connection.current.status).toBe("offline");
    env.clock.advance(60_000);
    expect(env.sources).toHaveLength(1);
    env.setOnline(true);
    expect(env.sources).toHaveLength(2);
    expect(connection.current.status).toBe("reconnecting");
  });

  test("a failure while offline says offline", () => {
    const {env, connection} = connect();
    env.navigator.onLine = false;
    env.latest().fail();
    expect(connection.current.status).toBe("offline");
  });

  test("a tab that comes back opens a closed or quiet stream at once, and leaves a live one", () => {
    const {env} = connect({staleAfter: 45_000});
    env.latest().fail();
    env.setVisible(false);
    env.setVisible(true);
    // Without waiting for the backoff.
    expect(env.sources).toHaveLength(2);
    env.latest().open();
    env.setVisible(true);
    expect(env.sources).toHaveLength(2);
  });

  test("reconnectNow starts the backoff over", () => {
    const {env, connection} = connect();
    env.latest().fail();
    env.clock.advance(1200);
    env.latest().fail();
    expect(connection.current.attempt).toBe(2);
    connection.reconnectNow();
    expect(connection.current).toEqual({status: "reconnecting", attempt: 0, nextAttemptAt: null});
    expect(env.sources).toHaveLength(3);
    // The backoff's timer is gone.
    env.clock.advance(60_000);
    expect(env.sources).toHaveLength(3);
  });

  test("each connection gets listeners of its own, and the URL read then", () => {
    let made = 0;
    let since = 0;
    const {env} = connect({url: () => `http://agentapi.test/events?since=${since}`, listeners: () => ({[`n${++made}`]: () => undefined})});
    env.latest().open();
    since = 7;
    env.latest().fail();
    expect(made).toBe(2);
    expect(env.latest().url).toBe("http://agentapi.test/events?since=7");
  });

  test("stop closes the stream and ends the timers and listeners", () => {
    const {env, connection} = connect({staleAfter: 45_000});
    env.latest().fail();
    connection.stop();
    expect(env.sources[0].closed).toBe(true);
    env.clock.advance(60_000);
    env.setOnline(true);
    env.setVisible(true);
    expect(env.sources).toHaveLength(1);
  });

  test("no URL, no stream", () => {
    const {env} = connect({url: () => null});
    expect(env.sources).toHaveLength(0);
  });
});
