import { memo, useCallback, useMemo, useSyncExternalStore } from 'react';
import { formatAgeSince } from '../../utils/formatters';
import { sharedClock } from '../../utils/sharedClock';

interface LiveAgeProps {
  timestamp: string;
}

const noop = () => () => {};

const LiveAge = memo(({ timestamp }: LiveAgeProps) => {
  const time = useMemo(() => new Date(timestamp).getTime(), [timestamp]);
  // The text is the snapshot, so a clock tick that leaves it as it was (any
  // age past a minute, most ticks) renders nothing.
  const getText = useCallback(
    () => (timestamp ? formatAgeSince(time, sharedClock.getSnapshot()) : '-'),
    [timestamp, time],
  );
  const text = useSyncExternalStore(
    timestamp ? sharedClock.subscribe : noop,
    getText,
    getText,
  );
  return <>{text}</>;
});

LiveAge.displayName = 'LiveAge';

export default LiveAge;
