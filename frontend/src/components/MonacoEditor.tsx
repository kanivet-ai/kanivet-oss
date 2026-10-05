import { lazyView } from '../utils/lazyView';

/*
  The editors, loaded from the bundled Monaco (utils/monacoSetup). Monaco is
  about 3 MB of code and 5 MB of heap once evaluated, and most sessions never
  open an editor, so it stays out of the idle prefetch: views warm it on
  intent with preloadEditor() (hovering "Edit YAML").
*/
const placeholder = ({
  height = '100%',
  width = '100%',
}: {
  height?: number | string;
  width?: number | string;
}) => <div style={{ height, width }} />;

export const Editor = lazyView(
  () => import('../utils/monacoSetup').then((m) => ({ default: m.Editor })),
  placeholder,
  { idlePrefetch: false },
);

export const DiffEditor = lazyView(
  () => import('../utils/monacoSetup').then((m) => ({ default: m.DiffEditor })),
  placeholder,
  { idlePrefetch: false },
);

/** Start loading Monaco; a failure here is retried when an editor renders. */
export const preloadEditor = () => {
  Editor.preload().catch(() => {});
};
