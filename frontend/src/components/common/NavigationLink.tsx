import React from 'react';
import {
  useNavigationLink,
  NavigationTarget,
} from '../../hooks/useNavigationLink';

interface NavigationLinkProps {
  target: NavigationTarget;
  children: React.ReactNode;
  className?: string;
  title?: string;
  as?: 'button' | 'span';
}

const NavigationLink: React.FC<NavigationLinkProps> = ({
  target,
  children,
  className = '',
  title,
  as = 'button',
}) => {
  const handleClick = useNavigationLink(target);

  if (as === 'span') {
    return (
      <span
        className={className}
        onClick={handleClick}
        title={title}
        style={{ cursor: 'pointer' }}
      >
        {children}
      </span>
    );
  }

  return (
    <button className={className} onClick={handleClick} title={title}>
      {children}
    </button>
  );
};

export default NavigationLink;
