/*
  Monaco from the app bundle instead of cdn.jsdelivr.net, where
  @monaco-editor/react fetches it by default: editors work offline and behind
  proxies, no remote code runs next to the preload bridge, and the editor
  ships in a lazy chunk (see components/MonacoEditor.tsx).

  Only the languages the editors can pick are included (see detectLanguage in
  the ConfigMap/Secret views).
*/
import { loader } from '@monaco-editor/react';
import * as monaco from 'monaco-editor/esm/vs/editor/editor.api';
import 'monaco-editor/esm/vs/editor/edcore.main';
import 'monaco-editor/esm/vs/basic-languages/yaml/yaml.contribution';
import 'monaco-editor/esm/vs/basic-languages/xml/xml.contribution';
import 'monaco-editor/esm/vs/basic-languages/html/html.contribution';
import 'monaco-editor/esm/vs/basic-languages/css/css.contribution';
import 'monaco-editor/esm/vs/basic-languages/javascript/javascript.contribution';
import 'monaco-editor/esm/vs/basic-languages/typescript/typescript.contribution';
import 'monaco-editor/esm/vs/basic-languages/shell/shell.contribution';
import 'monaco-editor/esm/vs/basic-languages/python/python.contribution';
import 'monaco-editor/esm/vs/basic-languages/sql/sql.contribution';
import 'monaco-editor/esm/vs/basic-languages/ini/ini.contribution';
import 'monaco-editor/esm/vs/language/json/monaco.contribution';
import EditorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker';
import JsonWorker from 'monaco-editor/esm/vs/language/json/json.worker?worker';

self.MonacoEnvironment = {
  getWorker: (_workerId: string, label: string) =>
    label === 'json' ? new JsonWorker() : new EditorWorker(),
};

loader.config({ monaco });
// The AMD build set this global; themeAdapter applies custom themes through it.
window.monaco = monaco;

export { default as Editor, DiffEditor } from '@monaco-editor/react';
