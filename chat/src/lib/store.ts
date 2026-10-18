import {useRef, useSyncExternalStore} from "react";

// Store holds a value that components read a part of with useStoreSelector:
// one renders again only when its part changes, not whenever the value does.
export class Store<T> {
  private value: T;
  private readonly listeners = new Set<() => void>();

  constructor(value: T) {
    this.value = value;
  }

  get = (): T => this.value;

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  set(value: T) {
    if (Object.is(value, this.value)) return;
    this.value = value;
    for (const listener of this.listeners) listener();
  }
}

// useStoreSelector returns selector's part of the store's value. The part
// keeps its object for as long as isEqual says it didn't change, and the
// component renders again only when it does.
export function useStoreSelector<T, S>(store: Store<T>, selector: (value: T) => S, isEqual: (a: S, b: S) => boolean = Object.is): S {
  const last = useRef<{value: T; selector: (value: T) => S; selected: S} | null>(null);
  const select = () => {
    const value = store.get();
    const previous = last.current;
    if (previous && Object.is(previous.value, value) && previous.selector === selector) return previous.selected;
    const selected = selector(value);
    if (previous && isEqual(previous.selected, selected)) {
      last.current = {value, selector, selected: previous.selected};
      return previous.selected;
    }
    last.current = {value, selector, selected};
    return selected;
  };
  return useSyncExternalStore(store.subscribe, select, select);
}
