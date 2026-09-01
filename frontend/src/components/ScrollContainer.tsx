import * as ScrollArea from '@radix-ui/react-scroll-area';
import './ScrollContainer.css';

interface ScrollContainerProps {
  children: React.ReactNode;
  className?: string;
  viewportClassName?: string;
  orientation?: 'vertical' | 'horizontal' | 'both';
}

const ScrollContainer = ({
  children,
  className = 'scroll-area',
  viewportClassName = 'viewport',
  orientation = 'vertical',
}: ScrollContainerProps) => (
  <ScrollArea.Root className={className}>
    <ScrollArea.Viewport className={viewportClassName}>
      {children}
    </ScrollArea.Viewport>
    {(orientation === 'vertical' || orientation === 'both') && (
      <ScrollArea.Scrollbar className="scrollbar" orientation="vertical">
        <ScrollArea.Thumb className="thumb" />
      </ScrollArea.Scrollbar>
    )}
    {(orientation === 'horizontal' || orientation === 'both') && (
      <ScrollArea.Scrollbar className="scrollbar" orientation="horizontal">
        <ScrollArea.Thumb className="thumb" />
      </ScrollArea.Scrollbar>
    )}
  </ScrollArea.Root>
);

export default ScrollContainer;
