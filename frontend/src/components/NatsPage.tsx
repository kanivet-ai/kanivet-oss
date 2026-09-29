import NatsOverview from './NatsOverview';

interface NatsPageProps {
  cluster: string;
}

const NatsPage = ({ cluster }: NatsPageProps) => {
  return <NatsOverview cluster={cluster} />;
};

export default NatsPage;
