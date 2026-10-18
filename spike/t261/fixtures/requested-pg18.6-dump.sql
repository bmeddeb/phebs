--
-- PostgreSQL database dump
--

\restrict phebsT261PinnedPG186Fixture

-- Dumped from database version 18.6 (Debian 18.6-1.pgdg13+2)
-- Dumped by pg_dump version 18.6 (Debian 18.6-1.pgdg13+2)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

SET default_table_access_method = heap;

--
-- Name: Dispatcher; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."Dispatcher" (
    id uuid NOT NULL,
    "createdAt" timestamp(3) without time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    "updatedAt" timestamp(3) without time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    "deletedAt" timestamp(3) without time zone,
    "lastHeartbeatAt" timestamp(3) without time zone,
    "isActive" boolean DEFAULT true NOT NULL
);


--
-- Name: Dispatcher Dispatcher_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."Dispatcher"
    ADD CONSTRAINT "Dispatcher_pkey" PRIMARY KEY (id);


--
-- PostgreSQL database dump complete
--

\unrestrict phebsT261PinnedPG186Fixture

