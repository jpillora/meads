let corePromise;

function instantiate(url, imports) {
  if (WebAssembly.instantiateStreaming) {
    return WebAssembly.instantiateStreaming(fetch(url), imports).catch(async () => {
      const bytes = await fetch(url).then((response) => {
        if (!response.ok) throw new Error(`Could not load Meads WASM (${response.status})`);
        return response.arrayBuffer();
      });
      return WebAssembly.instantiate(bytes, imports);
    });
  }
  return fetch(url)
    .then((response) => response.arrayBuffer())
    .then((bytes) => WebAssembly.instantiate(bytes, imports));
}

async function loadCore() {
  if (typeof globalThis.Go !== "function") {
    throw new Error("The Go WebAssembly runtime did not load");
  }
  const go = new globalThis.Go();
  const { instance } = await instantiate(new URL("./meads.wasm?v=0.41.1-204c6d", import.meta.url), go.importObject);
  void go.run(instance);
  if (typeof globalThis.meadsCoreApply !== "function") {
    throw new Error("The Meads WebAssembly core did not start");
  }
  return {
    apply(request) {
      const result = JSON.parse(globalThis.meadsCoreApply(JSON.stringify(request)));
      if (!result.ok) throw new Error(result.error || "Meads core rejected the change");
      return result.task;
    },
  };
}

export const meadsCore = {
  ready() {
    corePromise ||= loadCore();
    return corePromise;
  },
  async apply(request) {
    return (await this.ready()).apply(request);
  },
};
