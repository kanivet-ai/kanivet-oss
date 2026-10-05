import {
  Component,
  Fragment,
  lazy,
  Suspense,
  useState,
  type ComponentProps,
  type ComponentType,
  type ReactNode,
} from 'react';
import { whenIdle } from './idle';

/*
  Views that are not on the first screen live in their own chunks, so startup
  only parses what it paints. Once that has painted,
  prefetchLazyViewsWhenIdle() loads the chunks one per idle slot: by the time
  someone opens a view its code is already in, and it renders directly, with
  no Suspense fallback ("idle until urgent").

  `lazyView(() => import('./X'))` returns a component that renders X, and a
  `preload()` to warm it on intent.
*/

export type LazyView<C extends ComponentType<any>> = ComponentType<
  ComponentProps<C>
> & { preload: () => Promise<void> };

/** A view's chunk failed to load; its boundary offers a retry. */
class ChunkLoadError extends Error {
  constructor(cause: unknown) {
    super(cause instanceof Error ? cause.message : String(cause));
    this.name = 'ChunkLoadError';
  }
}

interface BoundaryState {
  error: unknown;
  attempt: number;
}

class ChunkBoundary extends Component<{ children: ReactNode }, BoundaryState> {
  state: BoundaryState = { error: null, attempt: 0 };

  static getDerivedStateFromError(error: unknown) {
    return { error };
  }

  render() {
    const { error, attempt } = this.state;
    // Only a failed chunk load is handled here; any other error keeps
    // propagating as if this boundary were not there.
    if (error && !(error instanceof ChunkLoadError)) throw error;
    if (error) {
      return (
        <div className="ap-load-error" role="alert">
          This view could not be loaded.
          <button
            type="button"
            className="ap-btn ap-btn--sm"
            onClick={() => this.setState({ error: null, attempt: attempt + 1 })}
          >
            Retry
          </button>
        </div>
      );
    }
    return <Fragment key={attempt}>{this.props.children}</Fragment>;
  }
}

const prefetchQueue: Array<() => Promise<void>> = [];
let prefetchStarted = false;
let prefetching = false;

const prefetchNext = () => {
  const preload = prefetchQueue.shift();
  if (!preload) {
    prefetching = false;
    return;
  }
  // A chunk that fails here is loaded again when its view is opened.
  preload().then(schedulePrefetch, schedulePrefetch);
};

const schedulePrefetch = () => {
  whenIdle(prefetchNext);
};

const drainPrefetchQueue = () => {
  if (!prefetchStarted || prefetching) return;
  prefetching = true;
  schedulePrefetch();
};

/** Load every lazy view, one per idle slot. Views defined later (inside a chunk loaded meanwhile) join the queue. */
export function prefetchLazyViewsWhenIdle(): void {
  prefetchStarted = true;
  drainPrefetchQueue();
}

export function lazyView<C extends ComponentType<any>>(
  load: () => Promise<{ default: C }>,
  fallback: ReactNode | ((props: ComponentProps<C>) => ReactNode) = null,
  { idlePrefetch = true }: { idlePrefetch?: boolean } = {},
): LazyView<C> {
  type P = ComponentProps<C>;
  let loaded: C | null = null;
  let pending: Promise<void> | null = null;
  const preload = (): Promise<void> =>
    (pending ??= load().then(
      (mod) => {
        loaded = mod.default;
      },
      (error) => {
        // Forget the failure so the next mount (or a retry) loads again,
        // instead of React.lazy replaying the cached rejection forever.
        pending = null;
        Lazy = makeLazy();
        throw new ChunkLoadError(error);
      },
    ));
  const makeLazy = () =>
    lazy(() => preload().then(() => ({ default: loaded as C })));
  let Lazy = makeLazy();

  const Body = (props: P) => {
    // Chosen once per mount: swapping the lazy wrapper for the loaded
    // component later would remount the view and drop its state.
    const [View] = useState(() => (loaded ?? Lazy) as ComponentType<P>);
    return <View {...props} />;
  };

  const LazyViewRoot = (props: P) => (
    <ChunkBoundary>
      <Suspense
        fallback={typeof fallback === 'function' ? fallback(props) : fallback}
      >
        <Body {...props} />
      </Suspense>
    </ChunkBoundary>
  );
  LazyViewRoot.preload = preload;
  if (idlePrefetch) {
    prefetchQueue.push(preload);
    drainPrefetchQueue();
  }
  return LazyViewRoot;
}
