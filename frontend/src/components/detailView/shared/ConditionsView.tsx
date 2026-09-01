import './ConditionsView.css';

const formatAge = (dateStr: string) => {
  if (!dateStr) return '-';
  const date = new Date(dateStr);
  const now = new Date();
  const diff = now.getTime() - date.getTime();
  const minutes = Math.floor(diff / 60000);
  const hours = Math.floor(minutes / 60);
  const days = Math.floor(hours / 24);
  if (days > 0) return `${days}d`;
  if (hours > 0) return `${hours}h`;
  return `${minutes}m`;
};

interface Condition {
  type: string;
  status: string;
  reason?: string;
  message?: string;
  lastTransitionTime?: string;
}

interface ConditionsViewProps {
  conditions: Condition[];
}

const ConditionsView = ({ conditions }: ConditionsViewProps) => {
  if (!conditions || conditions.length === 0) return null;

  return (
    <div className="conditions-list">
      {conditions.map((condition: Condition, idx: number) => (
        <div
          key={idx}
          className={`condition-item ${condition.status === 'True' ? 'status-true' : 'status-false'
            }`}
        >
          <div className="condition-header">
            <div className="condition-header-left">
              {condition.reason && (
                <span className="condition-type">{condition.type}</span>
              )}
              {!condition.reason && (
                <span className="condition-type-muted">{condition.type}</span>
              )}
              {condition.reason && (
                <span className="condition-reason">{condition.reason}</span>
              )}
            </div>
            <div className="condition-header-right">
              <span
                className={`condition-status ${condition.status === 'True' ? 'true' : 'false'
                  }`}
              >
                {condition.status}
              </span>
              <span className="condition-age">
                {condition.lastTransitionTime
                  ? formatAge(condition.lastTransitionTime)
                  : '-'}
              </span>
            </div>
          </div>
          {condition.message && (
            <div className="condition-message">{condition.message}</div>
          )}
        </div>
      ))}
    </div>
  );
};

export default ConditionsView;
