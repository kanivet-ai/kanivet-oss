import { useState, useEffect } from 'react';
import './SplashScreen.css';

const LOADING_MESSAGES = [
  'Kubernefying the system...',
  'Wrangling pods into formation...',
  'Spinning up the control plane...',
  'Consulting the etcd oracle...',
  'Teaching containers to behave...',
  'Negotiating with the scheduler...',
  'Summoning the cluster spirits...',
  'Deploying good vibes...',
  'Convincing nodes to cooperate...',
  'Orchestrating magnificence...',
  'Asking kubectl nicely...',
  'Herding microservices...',
  'Reconciling the universe...',
  'Waiting for pods to pod...',
  'Patching things together...',
  'Rolling out the red carpet...',
  'Scaling hopes and dreams...',
  'Checking if it works on my cluster...',
  'Draining bad vibes from nodes...',
  'Applying YAML with confidence...',
];

const getRandomMessage = (exclude: number) => {
  let next = Math.floor(Math.random() * LOADING_MESSAGES.length);
  while (next === exclude && LOADING_MESSAGES.length > 1) {
    next = Math.floor(Math.random() * LOADING_MESSAGES.length);
  }
  return next;
};

const SplashScreen = () => {
  const [messageIndex, setMessageIndex] = useState(() => Math.floor(Math.random() * LOADING_MESSAGES.length));

  useEffect(() => {
    const interval = setInterval(() => {
      setMessageIndex(prev => getRandomMessage(prev));
    }, 2500);
    return () => clearInterval(interval);
  }, []);

  return (
    <div className="splash-screen">
      <div className="splash-content">
        <div className="splash-logo">
          <span className="splash-k">K</span>
        </div>
        <h1 className="splash-title">Kanivet</h1>
        <div className="splash-loader">
          <div className="splash-loader-bar"></div>
        </div>
        <p className="splash-message">{LOADING_MESSAGES[messageIndex]}</p>
      </div>
    </div>
  );
};

export default SplashScreen;
