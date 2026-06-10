import "@testing-library/jest-dom";

class MemoryStorage implements Storage {
  readonly #data = new Map<string, string>();

  get length(): number {
    return this.#data.size;
  }

  clear(): void {
    this.#data.clear();
  }

  getItem(key: string): string | null {
    return this.#data.get(key) ?? null;
  }

  key(index: number): string | null {
    return Array.from(this.#data.keys())[index] ?? null;
  }

  removeItem(key: string): void {
    this.#data.delete(key);
  }

  setItem(key: string, value: string): void {
    this.#data.set(key, value);
  }
}

function installStorageShim(name: "localStorage" | "sessionStorage") {
  if (typeof window === "undefined" || typeof window[name]?.getItem === "function") {
    return;
  }

  Object.defineProperty(window, name, {
    configurable: true,
    value: new MemoryStorage(),
  });
  Object.defineProperty(globalThis, name, {
    configurable: true,
    value: window[name],
  });
}

installStorageShim("localStorage");
installStorageShim("sessionStorage");
