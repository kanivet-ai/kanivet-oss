import { Component, Fragment, type ErrorInfo, type ReactNode } from 'react';
import logger from '../../utils/logger';

interface Props {
  children: ReactNode;
  /** What the view shows, such as its tab and selection. A view that failed
   * renders again once this changes; a healthy one is left mounted. */
  resetKey?: unknown;
}

interface State {
  error: unknown;
  attempt: number;
  resetKey: unknown;
}

/**
 * Keeps a render error in one pane: the pane shows a compact card with a way
 * to render it again instead of the whole app going blank. A lazy view's
 * failed chunk load stays with its own ChunkBoundary, which offers a retry.
 */
export class ViewErrorBoundary extends Component<Props, State> {
  state: State = { error: null, attempt: 0, resetKey: this.props.resetKey };

  static getDerivedStateFromError(error: unknown): Partial<State> {
    return { error };
  }

  static getDerivedStateFromProps(
    props: Props,
    state: State,
  ): Partial<State> | null {
    if (Object.is(props.resetKey, state.resetKey)) return null;
    // Another tab or selection: a failed view gets a fresh mount.
    return state.error
      ? { error: null, attempt: state.attempt + 1, resetKey: props.resetKey }
      : { resetKey: props.resetKey };
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    logger.error('View failed to render', {
      error: error instanceof Error ? error.message : String(error),
      stack: error instanceof Error ? error.stack : undefined,
      componentStack: info.componentStack,
    });
  }

  reload = () =>
    this.setState((s) => ({ error: null, attempt: s.attempt + 1 }));

  render() {
    const { error, attempt } = this.state;
    if (error) {
      return (
        <div className="ap-load-error" role="alert" style={{ flex: 1 }}>
          This view ran into an error.
          <button
            type="button"
            className="ap-btn ap-btn--sm"
            onClick={this.reload}
          >
            Reload view
          </button>
        </div>
      );
    }
    return <Fragment key={attempt}>{this.props.children}</Fragment>;
  }
}
