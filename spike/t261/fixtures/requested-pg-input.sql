-- T26.1 supplementary public DDL fixture; no application database claim.
-- Exact complete Dispatcher CREATE TABLE from Hatchet MIT-licensed source.
-- Upstream commit559b5021e418f12ded175f950b709b7fa66be5a5 sql/schema/v0.sql.
-- Original copyright/license retained separately in licenses/hatchet.LICENSE.
CREATE TABLE "Dispatcher" (
    "id" UUID NOT NULL,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "deletedAt" TIMESTAMP(3),
    "lastHeartbeatAt" TIMESTAMP(3),
    "isActive" BOOLEAN NOT NULL DEFAULT true,

    CONSTRAINT "Dispatcher_pkey" PRIMARY KEY ("id")
);

