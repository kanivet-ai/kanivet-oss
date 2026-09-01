export type ResourceClickHandler = (
  kind: string,
  name: string,
  namespace?: string,
  e?: React.MouseEvent,
) => void;

export interface DetailViewProps {
  resource: any;
  handleResourceClick?: ResourceClickHandler;
}

export interface DetailViewPropsWithCluster extends DetailViewProps {
  cluster: string;
}
