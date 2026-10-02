// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { Route, Routes } from "react-router-dom";

import Layout from "./components/Layout";
import Device from "./pages/Device";
import AccessTokens from "./pages/iam/AccessTokens";
import Users from "./pages/iam/Users";
import Login from "./pages/Login";
import Overview from "./pages/Overview";
import Profile from "./pages/Profile";
import ResourceDetail from "./pages/ResourceDetail";
import ResourceForm from "./pages/ResourceForm";
import ResourceList from "./pages/ResourceList";
import Settings from "./pages/Settings";

export default function App() {
  return (
    <Routes>
      <Route path="login" element={<Login />} />
      <Route element={<Layout />}>
        <Route index element={<Overview />} />
        <Route path="profile" element={<Profile />} />
        <Route path="device" element={<Device />} />
        <Route path="settings" element={<Settings />} />
        <Route path="iam" element={<Users />} />
        <Route path="iam/tokens" element={<AccessTokens />} />
        <Route path=":kind" element={<ResourceList />} />
        <Route path=":kind/new" element={<ResourceForm />} />
        <Route path=":kind/:uid" element={<ResourceDetail />} />
        <Route path=":kind/:uid/edit" element={<ResourceForm />} />
      </Route>
    </Routes>
  );
}
