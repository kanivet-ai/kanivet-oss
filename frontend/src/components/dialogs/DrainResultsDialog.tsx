import '../common/Dialog.css';

interface DrainResultsDialogProps {
  results: any;
  onClose: () => void;
}

const DrainResultsDialog = ({ results, onClose }: DrainResultsDialogProps) => {
  const renderPodList = (
    pods: any[],
    title: string,
    color: string,
    showReason: boolean = false,
  ) => {
    if (!pods || pods.length === 0) return null;

    return (
      <div className="drain-results-section">
        <h4 className={`drain-results-title drain-results-${color}`}>
          {title} ({pods.length})
        </h4>
        <ul className="drain-results-list">
          {pods.map((pod: any, idx: number) => (
            <li key={idx} className="drain-results-item">
              <span className="drain-results-namespace">{pod.namespace}/</span>
              <span className="drain-results-name">{pod.name}</span>
              {showReason && pod.reason && (
                <span className={`drain-results-reason ${color === 'danger' ? 'drain-results-reason-danger' : ''}`}>
                  ({pod.reason})
                </span>
              )}
            </li>
          ))}
        </ul>
      </div>
    );
  };

  return (
    <div className="scale-dialog-overlay" onClick={onClose}>
      <div className="scale-dialog drain-results-dialog" onClick={(e) => e.stopPropagation()}>
        <h3>Drain Results</h3>
        <div className="scale-content">
          {renderPodList(results?.result?.deletedPods, 'Deleted Pods', 'success')}
          {renderPodList(results?.result?.skippedPods, 'Skipped Pods', 'warning', true)}
          {renderPodList(results?.result?.failedPods, 'Failed to Delete', 'danger', true)}
          {!results?.result?.deletedPods?.length &&
            !results?.result?.skippedPods?.length &&
            !results?.result?.failedPods?.length && (
              <p className="drain-results-empty">No pods were found on the node.</p>
            )}
        </div>
        <div className="scale-actions">
          <button onClick={onClose} className="scale-confirm-btn">OK</button>
        </div>
      </div>
    </div>
  );
};

export default DrainResultsDialog;
