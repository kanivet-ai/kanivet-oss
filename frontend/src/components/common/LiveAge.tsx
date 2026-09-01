import { memo, useSyncExternalStore } from 'react';
import { formatAge } from '../../utils/formatters';
import { sharedClock } from '../../utils/sharedClock';

interface LiveAgeProps {
  timestamp: string;
}

const noop = () => () => {};

const LiveAge = memo(({ timestamp }: LiveAgeProps) => {
  useSyncExternalStore(timestamp ? sharedClock.subscribe : noop, sharedClock.getSnapshot, sharedClock.getSnapshot);
  return <>{formatAge(timestamp)}</>;
});

LiveAge.displayName = 'LiveAge';

export default LiveAge;
