import { useEffect, useState } from 'react';
import { WEBSOCKET_URL } from "../constants";
import { Driver, Coordinate } from '../types';
import { PaymentEventSessionCreatedData, TripEvents, ServerWsMessage, isValidWsMessage, BackendEndpoints } from '../contracts';

export function useRiderStreamConnection(location: Coordinate, userID: string) {
  const [drivers, setDrivers] = useState<Driver[]>([]);
  const [tripStatus, setTripStatus] = useState<TripEvents | null>(null);
  const [paymentSession, setPaymentSession] = useState<PaymentEventSessionCreatedData | null>(null);
  const [assignedDriver, setAssignedDriver] = useState<Driver | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!userID) return;

    const ws = new WebSocket(`${WEBSOCKET_URL}${BackendEndpoints.WS_RIDERS}?userID=${userID}`);

    ws.onopen = () => {
      // Send initial location
      if (location) {
        ws.send(JSON.stringify({
          type: TripEvents.DriverLocation,
          data: {
            location,
          }
        }));
      }
    };

    ws.onmessage = (event) => {
      const message = JSON.parse(event.data) as ServerWsMessage;

      if (!message || !isValidWsMessage(message)) {
        console.warn('Ignoring unknown websocket message', message);
        return;
      }

      switch (message.type) {
        case TripEvents.DriverLocation:
          setDrivers(Array.isArray(message.data) ? message.data : []);
          break;
        case TripEvents.PaymentSessionCreated:
          setPaymentSession(message.data);
          setTripStatus(message.type);
          break;
        case TripEvents.DriverAssigned:
          setAssignedDriver(message.data?.driver ?? null);
          setTripStatus(message.type);
          break;
        case TripEvents.Created:
          setTripStatus(message.type);
          break;
        case TripEvents.NoDriversFound:
          setTripStatus(message.type);
          break;
      }
    };

    ws.onclose = () => {
      console.log('Rider WebSocket closed');
    };

    ws.onerror = (event) => {
      setError('WebSocket error occurred');
      console.error('WebSocket error:', event);
    };

    return () => {
      console.log('Closing WebSocket');
      if (ws.readyState === WebSocket.OPEN) {
        ws.close();
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userID]);

  const resetTripStatus = () => {
    setTripStatus(null);
    setPaymentSession(null);
    setAssignedDriver(null);
    setError(null);
  }

  return { drivers, assignedDriver, error, tripStatus, paymentSession, resetTripStatus };
}
