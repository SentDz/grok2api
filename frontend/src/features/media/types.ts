export type MediaAssetDTO = {
  id: string;
  kind: string;
  mimeType: string;
  sizeBytes: number;
  sha256: string;
  createdAt: string;
  url: string;
};

export type MediaJobDTO = {
  id: string;
  model: string;
  prompt: string;
  status: "queued" | "in_progress" | "completed" | "failed";
  progress: number;
  seconds: number;
  size: string;
  quality: string;
  accountName: string;
  clientKeyName: string;
  createdAt: string;
  completedAt: string | null;
  errorMessage: string;
  assetId: string;
};

export type ImageStatsDTO = { totalImages: number; totalBytes: number };

export type VideoTaskEvent = {
  stage: string;
  startedAt: string;
  finishedAt: string | null;
  attempt: number;
  accountId: number;
  accountName: string;
  itemIndex: number;
  itemTotal: number;
  error: string;
  httpStatus: number;
  request?: string;
  egressNodeId?: number;
  egressNodeName?: string;
  egressScope?: string;
  egressMode?: string;
  deadlineAt?: string;
};

export type VideoJobDetailDTO = MediaJobDTO & {
  diagnosticsEnabled: boolean;
  requestId: string;
  provider: string;
  upstreamModel: string;
  accountId: number;
  egressNodeName: string;
  egressMode: string;
  errorCode: string;
  updatedAt: string;
  leaseUntil: string | null;
  serverTime: string;
  diagnostics: { attempt: number; events: VideoTaskEvent[] | null };
};
export type VideoStatsDTO = { totalJobs: number; completed: number; failed: number; inProgress: number; queued: number };
