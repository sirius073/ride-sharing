'use client';

import Image from 'next/image';
import { useRiderStreamConnection } from '../hooks/useRiderStreamConnection';
import { MapContainer, Marker, Popup, Rectangle, TileLayer } from 'react-leaflet'
import L from 'leaflet';
import { getGeohashBounds } from '../utils/geohash';
import { useMemo, useRef, useState } from 'react';
import { MapClickHandler } from './MapClickHandler';
import { RouteFare, RequestRideProps, TripPreview, HTTPTripStartResponse } from "../types";
import { RoutingControl } from "./RoutingControl";
import { API_URL } from '../constants';
import { RiderTripOverview } from './RiderTripOverview';
import { BackendEndpoints, HTTPTripPreviewRequestPayload, HTTPTripPreviewResponse, HTTPTripStartRequestPayload } from '../contracts';

const riderMarker = L.divIcon({
    className: "",
    html: `<div style="width:18px;height:18px;border-radius:9999px;background:#2563eb;border:3px solid #ffffff;box-shadow:0 0 0 2px rgba(37,99,235,0.35)"></div>`,
    iconSize: [18, 18],
    iconAnchor: [9, 9],
});

const destinationMarker = L.divIcon({
    className: "",
    html: `<div style="width:18px;height:18px;border-radius:9999px;background:#dc2626;border:3px solid #ffffff;box-shadow:0 0 0 2px rgba(220,38,38,0.35)"></div>`,
    iconSize: [18, 18],
    iconAnchor: [9, 9],
});

const driverMarker = new L.Icon({
    iconUrl: "https://www.svgrepo.com/show/25407/car.svg",
    iconSize: [36, 36],
    iconAnchor: [18, 18],
});

interface RiderMapProps {
    onRouteSelected?: (distance: number) => void;
}

export default function RiderMap({ onRouteSelected }: RiderMapProps) {
    const [trip, setTrip] = useState<TripPreview | null>(null)
    const [destination, setDestination] = useState<[number, number] | null>(null)
    const [isLoadingPreview, setIsLoadingPreview] = useState(false)
    const [isStartingTrip, setIsStartingTrip] = useState(false)
    const mapRef = useRef<L.Map>(null)
    const userID = useMemo(() => crypto.randomUUID(), [])
    const debounceTimeoutRef = useRef<NodeJS.Timeout | null>(null);

    const location = {
        latitude: 37.7749,
        longitude: -122.4194,
    };

    const {
        drivers,
        error,
        tripStatus,
        assignedDriver,
        paymentSession,
        resetTripStatus
    } = useRiderStreamConnection(location, userID);

    const handleMapClick = async (e: L.LeafletMouseEvent) => {
        if (trip?.tripID) {
            return
        }

        if (debounceTimeoutRef.current) {
            clearTimeout(debounceTimeoutRef.current);
        }

        debounceTimeoutRef.current = setTimeout(async () => {
            setIsLoadingPreview(true)
            try {
                setDestination([e.latlng.lat, e.latlng.lng])

                const data = await requestRidePreview({
                    pickup: [location.latitude, location.longitude],
                    destination: [e.latlng.lat, e.latlng.lng],
                })

                const parsedRoute = data.route.geometry[0]?.coordinates
                    ?.map((coord) => [coord.latitude, coord.longitude] as [number, number]) ?? []

                setTrip({
                    tripID: "",
                    route: parsedRoute,
                    rideFares: data.rideFares,
                    distance: data.route.distance,
                    duration: data.route.duration,
                })

                onRouteSelected?.(data.route.distance)
            } catch (previewError) {
                console.error('Failed to load trip preview', previewError)
                alert('Failed to preview trip. Please try clicking the map again.')
            } finally {
                setIsLoadingPreview(false)
            }
        }, 500);
    }

    const requestRidePreview = async (props: RequestRideProps): Promise<HTTPTripPreviewResponse> => {
        const { pickup, destination } = props
        const payload = {
            userID: userID,
            pickup: {
                latitude: pickup[0],
                longitude: pickup[1],
            },
            destination: {
                latitude: destination[0],
                longitude: destination[1],
            },
        } as HTTPTripPreviewRequestPayload

        const response = await fetch(`${API_URL}${BackendEndpoints.PREVIEW_TRIP}`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            },
            body: JSON.stringify(payload),
        })
        if (!response.ok) {
            throw new Error(`Preview request failed with status ${response.status}`)
        }
        const { data } = await response.json() as { data: HTTPTripPreviewResponse }
        return data
    }

    const handleStartTrip = async (fare: RouteFare) => {
        const payload = {
            rideFareID: fare.id,
            userID: userID,
        } as HTTPTripStartRequestPayload

        if (!fare.id) {
            alert("No Fare ID in the payload")
            return
        }

        setIsStartingTrip(true)
        try {
            const response = await fetch(`${API_URL}${BackendEndpoints.START_TRIP}`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify(payload),
            })
            const data = await response.json() as HTTPTripStartResponse

            if (!response.ok) {
                throw new Error(`Start trip failed with status ${response.status}`)
            }

            if (trip) {
                setTrip((prev) => ({
                    ...prev,
                    tripID: data.tripID,
                } as TripPreview))
            }

            return data
        } catch (startError) {
            console.error('Failed to start trip', startError)
            alert('Failed to start trip. Please select your package again.')
            return
        } finally {
            setIsStartingTrip(false)
        }
    }

    const handleCancelTrip = () => {
        setTrip(null)
        setDestination(null)
        resetTripStatus()
        setIsStartingTrip(false)
        setIsLoadingPreview(false)
    }

    if (error) {
        return <div>Error: {error}</div>
    }

    return (
        <div className="relative flex flex-col md:flex-row h-screen">
            <div className={`${destination ? 'flex-[0.7]' : 'flex-1'}`}>
                <MapContainer
                    center={[location.latitude, location.longitude]}
                    zoom={13}
                    style={{ height: '100%', width: '100%' }}
                    ref={mapRef}
                >
                    <TileLayer
                        url="https://{s}.basemaps.cartocdn.com/light_all/{z}/{x}/{y}{r}.png"
                        attribution="&copy; <a href='https://www.openstreetmap.org/copyright'>OpenStreetMap</a> contributors &copy; <a href='https://carto.com/'>CARTO</a>"
                    />
                    <Marker position={[location.latitude, location.longitude]} icon={riderMarker}>
                        <Popup>Pickup location</Popup>
                    </Marker>

                    {/* Render geohash grid cells */}
                    {drivers?.map((driver) => (
                        <Rectangle
                            key={`grid-${driver?.geohash}`}
                            bounds={getGeohashBounds(driver?.geohash) as L.LatLngBoundsExpression}
                            pathOptions={{
                                color: '#3388ff',
                                weight: 1,
                                fillOpacity: 0.1
                            }}
                        >
                            <Popup>Geohash: {driver?.geohash}</Popup>
                        </Rectangle>
                    ))}

                    {/* Render driver markers */}
                    {drivers?.map((driver) => (
                        <Marker
                            key={driver?.id}
                            position={[driver?.location?.latitude, driver?.location?.longitude]}
                            icon={driverMarker}
                        >
                            <Popup>
                                Driver ID: {driver?.id}
                                <br />
                                Geohash: {driver?.geohash}
                                <br />
                                Name: {driver?.name}
                                <br />
                                Car Plate: {driver?.carPlate}
                                <br />
                                <Image
                                    src={driver?.profilePicture}
                                    alt={`${driver?.name}'s profile picture`}
                                    width={100}
                                    height={100}
                                />
                            </Popup>
                        </Marker>
                    ))}
                    {destination && (
                        <Marker position={destination} icon={destinationMarker}>
                            <Popup>Destination</Popup>
                        </Marker>
                    )}

                    {trip && (
                        <RoutingControl route={trip.route} />
                    )}
                    <MapClickHandler onClick={handleMapClick} />
                </MapContainer>
            </div>

            <div className="flex-[0.4]">
                <RiderTripOverview
                    trip={trip}
                    assignedDriver={assignedDriver}
                    status={tripStatus}
                    paymentSession={paymentSession}
                    onPackageSelect={handleStartTrip}
                    onCancel={handleCancelTrip}
                    isLoadingPreview={isLoadingPreview}
                    isStartingTrip={isStartingTrip}
                />
            </div>
        </div>
    )
}