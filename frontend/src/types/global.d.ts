interface ImportMetaEnv {
  readonly DEV?: boolean;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}

interface Window {
  monaco: any;
  electron?: {
    openExternal: (url: string) => void;
    log: (message: string) => void;
    isDev: boolean;
  };
}

// Vite's `?worker` imports (the Monaco workers in utils/monacoSetup).
declare module '*?worker' {
  const WorkerConstructor: { new (): Worker };
  export default WorkerConstructor;
}
